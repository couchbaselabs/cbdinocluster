package clouddeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/multierr"

	"github.com/couchbase/gocbcorex/cbqueryx"
	"github.com/couchbaselabs/cbdinocluster/utils/webhelper"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// Deployer drives Capella through two APIs.
//
// Operational clusters use the public Management API v4 (v4 and v4mgr), whose
// API key credential is stateless. The internal v2 API (client and mgr) is used
// only where v4 has no equivalent: custom server image deployment, server
// version overrides, the internal support endpoints, and columnar specific
// features. Authenticating against v2 invalidates any other active session for
// the same user, so v2 must stay off the common path.
type Deployer struct {
	logger                   *zap.Logger
	client                   *capellacontrol.Controller
	mgr                      *capellacontrol.Manager
	v4                       *capellav4.Client
	v4mgr                    *capellav4.Manager
	hasLegacyCredentials     bool
	tenantID                 string
	overrideToken            string
	internalSupportToken     string
	defaultCloud             string
	defaultAwsRegion         string
	defaultAzureRegion       string
	defaultGcpRegion         string
	uploadServerLogsHostName string
}

var _ deployment.Deployer = (*Deployer)(nil)

type NewDeployerOptions struct {
	Logger   *zap.Logger
	Client   *capellacontrol.Controller
	V4Client *capellav4.Client
	// HasLegacyCredentials reports whether v2 username and password are
	// configured. Without them, operations that only v2 can serve fail early
	// with an explanation instead of a bare authentication error.
	HasLegacyCredentials     bool
	TenantID                 string
	OverrideToken            string
	InternalSupportToken     string
	DefaultCloud             string
	DefaultAwsRegion         string
	DefaultAzureRegion       string
	DefaultGcpRegion         string
	UploadServerLogsHostName string
}

func NewDeployer(opts *NewDeployerOptions) (*Deployer, error) {
	if opts.V4Client == nil {
		return nil, errors.New("a capella v4 client is required")
	}

	return &Deployer{
		logger: opts.Logger,
		client: opts.Client,
		mgr: &capellacontrol.Manager{
			Logger: opts.Logger,
			Client: opts.Client,
		},
		v4: opts.V4Client,
		v4mgr: &capellav4.Manager{
			Logger: opts.Logger,
			Client: opts.V4Client,
		},
		hasLegacyCredentials:     opts.HasLegacyCredentials,
		tenantID:                 opts.TenantID,
		overrideToken:            opts.OverrideToken,
		internalSupportToken:     opts.InternalSupportToken,
		defaultCloud:             opts.DefaultCloud,
		defaultAwsRegion:         opts.DefaultAwsRegion,
		defaultAzureRegion:       opts.DefaultAzureRegion,
		defaultGcpRegion:         opts.DefaultGcpRegion,
		uploadServerLogsHostName: opts.UploadServerLogsHostName,
	}, nil
}

// requireLegacy guards the operations that only the internal v2 API can serve.
// It names the feature so the failure explains why a session is needed.
func (p *Deployer) requireLegacy(feature string) error {
	if p.hasLegacyCredentials {
		return nil
	}
	return errors.Errorf("%s needs the internal capella v2 api, which requires a "+
		"username and password; note that authenticating there invalidates other "+
		"active sessions for the same user", feature)
}

// clusterInfo pairs a cbdc2 project with the single cluster it contains.
//
// cbdinocluster encodes the cluster ID and expiry in the project name, so the
// project is the unit of ownership and exactly one cluster is expected inside it.
//
// The v4 cluster object carries no project reference, so ProjectID is tracked
// here rather than read back off the cluster.
type clusterInfo struct {
	Meta        *stringclustermeta.MetaData
	ProjectID   string
	ProjectName string
	Cluster     *capellav4.ClusterInfo
	Columnar    *capellav4.AnalyticsClusterInfo
	IsCorrupted bool
}

// cbdc2Project is a project whose name carries cbdinocluster metadata.
type cbdc2Project struct {
	Meta *stringclustermeta.MetaData
	Info *capellav4.ProjectInfo
}

// listCbdc2Projects returns only the projects cbdinocluster owns. Projects
// belonging to other users are skipped, which also keeps the per-project cluster
// listing below proportional to our own usage rather than the whole organization.
func (p *Deployer) listCbdc2Projects(ctx context.Context) ([]cbdc2Project, error) {
	p.logger.Debug("listing cloud projects")

	projects, err := p.v4.ListProjects(ctx, p.tenantID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list projects")
	}

	var out []cbdc2Project
	for _, project := range projects {
		meta, err := stringclustermeta.Parse(project.Name)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse meta-data from project name")
		}
		if meta == nil {
			continue
		}

		out = append(out, cbdc2Project{Meta: meta, Info: project})
	}

	return out, nil
}

// inspectProject resolves the single cluster inside one cbdc2 project. It returns
// nil when the project holds no cluster yet, which happens between project
// creation and cluster creation.
func (p *Deployer) inspectProject(ctx context.Context, project cbdc2Project) (*clusterInfo, error) {
	base := &clusterInfo{
		Meta:        project.Meta,
		ProjectID:   project.Info.ID,
		ProjectName: project.Info.Name,
	}

	clusters, err := p.v4.ListClusters(ctx, p.tenantID, project.Info.ID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters for project")
	}

	if len(clusters) > 1 {
		base.IsCorrupted = true
		return base, nil
	}
	if len(clusters) == 1 {
		base.Cluster = clusters[0]
		return base, nil
	}

	// Analytics clusters live under a separate collection, so an empty cluster
	// list does not mean the project is empty.
	columnars, err := p.v4.ListAnalyticsClusters(ctx, p.tenantID, project.Info.ID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list analytics clusters for project")
	}

	if len(columnars) > 1 {
		base.IsCorrupted = true
		return base, nil
	}
	if len(columnars) == 1 {
		base.Columnar = columnars[0]
		return base, nil
	}

	return base, nil
}

func (p *Deployer) listClusters(ctx context.Context) ([]*clusterInfo, error) {
	projects, err := p.listCbdc2Projects(ctx)
	if err != nil {
		return nil, err
	}

	p.logger.Debug("listing cloud clusters", zap.Int("projects", len(projects)))

	var out []*clusterInfo
	for _, project := range projects {
		info, err := p.inspectProject(ctx, project)
		if err != nil {
			return nil, err
		}

		out = append(out, info)
	}

	return out, nil
}

func (p *Deployer) getCluster(ctx context.Context, clusterID string) (*clusterInfo, error) {
	projects, err := p.listCbdc2Projects(ctx)
	if err != nil {
		return nil, err
	}

	// The cluster ID is encoded in the project name, so the owning project is
	// identified without listing clusters across the organization.
	var foundProject *cbdc2Project
	for _, project := range projects {
		if project.Meta.ID.String() == clusterID {
			foundProject = &project
			break
		}
	}
	if foundProject == nil {
		return nil, errors.New("failed to find cluster")
	}

	foundCluster, err := p.inspectProject(ctx, *foundProject)
	if err != nil {
		return nil, err
	}

	if foundCluster.IsCorrupted {
		return nil, errors.New("found cluster, but it is in a corrupted state")
	}

	if foundCluster.Cluster == nil && foundCluster.Columnar == nil {
		return nil, errors.New("found cluster, but it has no cluster provisioned yet")
	}

	return foundCluster, nil
}

// columnarV2Detail fetches the internal v2 record for a columnar cluster.
//
// The v4 analytics API exposes no connection string, certificate or database
// credentials, so those operations still need v2. This is deliberately lazy: it
// is only called by columnar specific operations, which keeps operational cluster
// commands free of a v2 session.
func (p *Deployer) columnarV2Detail(ctx context.Context, info *clusterInfo) (*capellacontrol.ColumnarData, error) {
	if info.Columnar == nil {
		return nil, errors.New("cluster is not a columnar cluster")
	}
	return p.columnarV2DetailByID(ctx, info.Columnar.ID)
}

func (p *Deployer) columnarV2DetailByID(ctx context.Context, columnarID string) (*capellacontrol.ColumnarData, error) {
	if err := p.requireLegacy("this columnar operation"); err != nil {
		return nil, err
	}

	columnars, err := p.client.ListAllColumnars(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list columnars")
	}

	for _, columnar := range columnars.Data {
		if columnar.Data.ID == columnarID {
			return columnar.Data, nil
		}
	}

	return nil, errors.New("failed to find columnar instance")
}

func (p *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	clusters, err := p.listClusters(ctx)
	if err != nil {
		return nil, err
	}

	var out []deployment.ClusterInfo

	for _, cluster := range clusters {
		if cluster.IsCorrupted {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID.String(),
				Type:           deployment.ClusterTypeUnknown,
				CloudProjectID: cluster.ProjectID,
				CloudClusterID: "",
				CloudProvider:  "",
				Region:         "",
				Expiry:         cluster.Meta.Expiry,
				State:          "corrupted",
			})
			continue
		} else if cluster.Cluster == nil && cluster.Columnar == nil {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID.String(),
				Type:           deployment.ClusterTypeUnknown,
				CloudProjectID: cluster.ProjectID,
				CloudClusterID: "",
				CloudProvider:  "",
				Region:         "",
				Expiry:         cluster.Meta.Expiry,
				State:          "provisioning",
			})
			continue
		}

		if cluster.Cluster != nil {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID.String(),
				Type:           deployment.ClusterTypeServer,
				CloudProjectID: cluster.ProjectID,
				CloudClusterID: cluster.Cluster.ID,
				CloudProvider:  cluster.Cluster.CloudProvider.Type,
				Region:         cluster.Cluster.CloudProvider.Region,
				Expiry:         cluster.Meta.Expiry,
				State:          cluster.Cluster.CurrentState,
			})
		} else if cluster.Columnar != nil {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID.String(),
				Type:           deployment.ClusterTypeColumnar,
				CloudProjectID: cluster.ProjectID,
				CloudClusterID: cluster.Columnar.ID,
				CloudProvider:  cluster.Columnar.CloudProviderName(),
				Region:         cluster.Columnar.Region,
				Expiry:         cluster.Meta.Expiry,
				State:          cluster.Columnar.CurrentState,
			})
		}
	}

	return out, nil
}

type NewClusterNodeGroupOptions struct {
	Count        int
	Services     []clusterdef.Service
	InstanceType string
	DiskType     string
	DiskSize     int
	DiskIops     int
}

type NewClusterOptions struct {
	Expiry     time.Duration
	Cidr       string
	Version    string
	NodeGroups []*NewClusterNodeGroupOptions
}

func (p *Deployer) buildDeploySpecs(
	ctx context.Context,
	cloudProvider string,
	nodeGrps []*clusterdef.NodeGroup,
) ([]capellacontrol.DeployClusterRequest_Spec, error) {
	diskAutoExpansionEnabled := false
	if cloudProvider == "aws" {
		diskAutoExpansionEnabled = true
	} else if cloudProvider == "gcp" {
		diskAutoExpansionEnabled = true
	} else if cloudProvider == "azure" {
		diskAutoExpansionEnabled = false
	} else {
		return nil, errors.New("invalid cloud provider for setup info")
	}

	var specs []capellacontrol.DeployClusterRequest_Spec
	for _, nodeGroup := range nodeGrps {
		var instanceType string
		var cpu int
		var memory int
		var diskType string
		var diskSize int
		var diskIops int

		if cloudProvider == "aws" {
			instanceType = "m5.xlarge"
			cpu = 4
			memory = 16
			diskType = "gp3"
			diskSize = 50
			diskIops = 3000
		} else if cloudProvider == "gcp" {
			instanceType = "n2-standard-4"
			cpu = 4
			memory = 16
			diskType = "pd-ssd"
			diskSize = 50
		} else if cloudProvider == "azure" {
			instanceType = "Standard_D4s_v5"
			cpu = 4
			memory = 16
			diskType = "P6"
			diskSize = 64
			diskIops = 240
		} else {
			return nil, errors.New("invalid cloud provider specified")
		}

		if nodeGroup.Cloud.InstanceType != "" {
			instanceType = nodeGroup.Cloud.InstanceType
		}
		if nodeGroup.Cloud.DiskType != "" {
			diskType = nodeGroup.Cloud.DiskType
		}
		if nodeGroup.Cloud.DiskSize != 0 {
			diskSize = nodeGroup.Cloud.DiskSize
		}
		if nodeGroup.Cloud.DiskIops != 0 {
			diskIops = nodeGroup.Cloud.DiskIops
		}
		if nodeGroup.Cloud.Cpu != 0 {
			cpu = nodeGroup.Cloud.Cpu
		}
		if nodeGroup.Cloud.Memory != 0 {
			memory = nodeGroup.Cloud.Memory
		}

		services := []clusterdef.Service{
			clusterdef.KvService,
			clusterdef.IndexService,
			clusterdef.QueryService,
			clusterdef.SearchService,
		}
		if len(nodeGroup.Services) > 0 {
			services = nodeGroup.Services
		}

		nsServiceNames, err := clusterdef.ServicesToNsServices(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

		nsServices := lo.Map(nsServiceNames, func(name string, _ int) capellacontrol.CreateServices {
			return capellacontrol.CreateServices{Type: name}
		})

		specs = append(specs, capellacontrol.DeployClusterRequest_Spec{
			Compute: capellacontrol.DeployClusterRequest_Spec_Compute{
				Type:   instanceType,
				Cpu:    cpu,
				Memory: memory,
			},
			Count: nodeGroup.Count,
			Disk: capellacontrol.CreateClusterRequest_Spec_Disk{
				Type:     diskType,
				SizeInGb: diskSize,
				Iops:     diskIops,
			},
			DiskAutoScaling: capellacontrol.CreateClusterRequest_Spec_DiskScaling{
				Enabled: diskAutoExpansionEnabled,
			},
			Services: nsServices,
		})
	}

	return specs, nil
}

// deployNewCluster provisions a cluster from a specific server image. Only the
// internal v2 API can override the image, so this whole path needs v2.
func (p *Deployer) deployNewCluster(ctx context.Context, def *clusterdef.Cluster, clusterVersion string, serverImage string) (deployment.ClusterInfo, error) {
	if err := p.requireLegacy("custom server image deployment"); err != nil {
		return nil, err
	}

	clusterID := cbdcuuid.New()

	expiryTime := time.Time{}
	if def.Expiry > 0 {
		expiryTime = time.Now().Add(def.Expiry)
	}

	metaData := stringclustermeta.MetaData{
		ID:     clusterID,
		Expiry: expiryTime,
	}
	projectName := metaData.String()

	p.logger.Debug("creating a new cloud project")

	newProject, err := p.v4.CreateProject(ctx, p.tenantID, &capellav4.CreateProjectRequest{
		Name: projectName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create project")
	}

	cloudProjectID := newProject.ID

	cloudProvider, cloudRegion, err := p.resolveCloudLocation(def)
	if err != nil {
		return nil, err
	}

	clusterCidr := def.Cloud.Cidr

	deploymentProvider := ""
	clusterProvider := ""
	if cloudProvider == "aws" {
		deploymentProvider = "aws"
		clusterProvider = "hostedAWS"
	} else if cloudProvider == "gcp" {
		deploymentProvider = "gcp"
		clusterProvider = "hostedGCP"
	} else if cloudProvider == "azure" {
		deploymentProvider = "azure"
		clusterProvider = "hostedAzure"
	} else {
		return nil, errors.New("invalid cloud provider for setup info")
	}

	p.logger.Debug("fetching deployment options project")

	deploymentOpts, err := p.client.GetProviderDeploymentOptions(ctx, p.tenantID, &capellacontrol.GetProviderDeploymentOptionsRequest{
		Provider: deploymentProvider,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get deployment options")
	}

	if clusterVersion == "" {
		clusterVersion = deploymentOpts.ServerVersions.DefaultOptionKey
	}
	if clusterCidr == "" {
		clusterCidr = deploymentOpts.CIDR.SuggestedBlock
	}

	p.logger.Debug("creating a new cloud cluster")

	clusterName := fmt.Sprintf("cbdc2_%s", clusterID)

	specs, err := p.buildDeploySpecs(
		ctx,
		cloudProvider,
		def.NodeGroups)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build cluster specs")
	}

	createReq := &capellacontrol.DeployClusterRequest{
		CIDR:        clusterCidr,
		Description: "",
		Name:        clusterName,
		Package:     "developerPro",
		ProjectId:   cloudProjectID,
		TenantId:    p.tenantID,
		Provider:    clusterProvider,
		Region:      cloudRegion,
		Override: capellacontrol.CreateOverrideRequest{
			Image:  serverImage,
			Server: clusterVersion,
			Token:  p.overrideToken,
		},
		Server:   clusterVersion,
		SingleAZ: false,
		Specs:    specs,
		Timezone: "PT",
	}

	p.logger.Debug("creating cluster", zap.Any("req", createReq))

	newCluster, err := p.client.DeployCluster(ctx, p.tenantID, createReq)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cluster")
	}

	cloudClusterID := newCluster.Id

	p.logger.Debug("waiting for cluster creation to complete")

	err = p.mgr.WaitForClusterState(ctx, p.tenantID, cloudClusterID, "healthy", false)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for cluster deployment")
	}

	// we cheat for now...
	clusters, err := p.ListClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}

	var thisCluster *ClusterInfo
	for _, cluster := range clusters {
		cluster := cluster.(*ClusterInfo)

		if cluster.ClusterID == clusterID.String() {
			thisCluster = cluster
		}
	}
	if thisCluster == nil {
		return nil, errors.New("failed to find new cluster after deployment")
	}

	return thisCluster, nil
}

// resolveCloudLocation picks the cloud provider and region for a new cluster,
// falling back to the defaults from the cbdinocluster config.
func (p *Deployer) resolveCloudLocation(def *clusterdef.Cluster) (string, string, error) {
	cloudProvider := def.Cloud.CloudProvider
	if cloudProvider == "" {
		cloudProvider = p.defaultCloud
	}

	cloudRegion := def.Cloud.Region
	if cloudRegion == "" {
		switch cloudProvider {
		case capellav4.ProviderAws:
			cloudRegion = p.defaultAwsRegion
		case capellav4.ProviderAzure:
			cloudRegion = p.defaultAzureRegion
		case capellav4.ProviderGcp:
			cloudRegion = p.defaultGcpRegion
		default:
			return "", "", errors.New("invalid cloud provider for region selection")
		}
	}

	return cloudProvider, cloudRegion, nil
}

func (p *Deployer) createNewCluster(ctx context.Context, def *clusterdef.Cluster, clusterVersion string) (deployment.ClusterInfo, error) {
	clusterID := cbdcuuid.New()

	expiryTime := time.Time{}
	if def.Expiry > 0 {
		expiryTime = time.Now().Add(def.Expiry)
	}

	metaData := stringclustermeta.MetaData{
		ID:     clusterID,
		Expiry: expiryTime,
	}
	projectName := metaData.String()

	cloudProvider, cloudRegion, err := p.resolveCloudLocation(def)
	if err != nil {
		return nil, err
	}

	p.logger.Debug("creating a new cloud project")

	newProject, err := p.v4.CreateProject(ctx, p.tenantID, &capellav4.CreateProjectRequest{
		Name: projectName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create project")
	}

	cloudProjectID := newProject.ID

	p.logger.Debug("creating a new cloud cluster")

	clusterName := fmt.Sprintf("cbdc2_%s", clusterID)

	// An empty CIDR or server version makes Capella choose a free block and the
	// current default version, which is what the v2 deployment options lookup used
	// to do by hand.
	cloudProviderSpec := capellav4.CloudProvider{
		Type:   cloudProvider,
		Region: cloudRegion,
		Cidr:   def.Cloud.Cidr,
	}

	cloudClusterID := ""
	if def.Cloud.FreeTier {
		if len(def.NodeGroups) != 0 {
			return nil, errors.New("free-tier cluster cannot have node groups")
		}

		createReq := &capellav4.CreateFreeTierClusterRequest{
			Name:          clusterName,
			CloudProvider: cloudProviderSpec,
		}
		p.logger.Debug("creating free tier cluster", zap.Any("req", createReq))

		newCluster, err := p.v4.CreateFreeTierCluster(ctx, p.tenantID, cloudProjectID, createReq)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create cluster")
		}

		cloudClusterID = newCluster.ID

		p.logger.Debug("waiting for creation to complete")

		err = p.v4mgr.WaitForClusterState(ctx, p.tenantID, cloudProjectID, cloudClusterID, capellav4.StateHealthy)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wait for deployment")
		}
	} else if !def.Columnar {
		serviceGroups, err := buildServiceGroups(cloudProvider, def.NodeGroups)
		if err != nil {
			return nil, errors.Wrap(err, "failed to build cluster specs")
		}

		createReq := &capellav4.CreateClusterRequest{
			Name:          clusterName,
			CloudProvider: cloudProviderSpec,
			ServiceGroups: serviceGroups,
			Availability: capellav4.Availability{
				Type: capellav4.AvailabilityMulti,
			},
			Support: capellav4.Support{
				Plan:     "developer pro",
				Timezone: "PT",
			},
		}
		if clusterVersion != "" {
			createReq.CouchbaseServer = &capellav4.CouchbaseServer{
				Version: clusterVersion,
			}
		}
		p.logger.Debug("creating cluster", zap.Any("req", createReq))

		newCluster, err := p.v4.CreateCluster(ctx, p.tenantID, cloudProjectID, createReq)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create cluster")
		}

		cloudClusterID = newCluster.ID

		p.logger.Debug("waiting for creation to complete")

		err = p.v4mgr.WaitForClusterState(ctx, p.tenantID, cloudProjectID, cloudClusterID, capellav4.StateHealthy)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wait for deployment")
		}
	} else {
		// The v4 analytics API cannot create a cluster with a specific image, and
		// exposes no credentials or connection string, so columnar stays on v2.
		if err := p.requireLegacy("columnar cluster deployment"); err != nil {
			return nil, err
		}

		if len(def.NodeGroups) > 1 {
			return nil, errors.New("columnar only supports 1 node group")
		}

		nodeCount := 1
		cpu := 4
		memory := 32
		if def.NodeGroups[0].Count != 0 {
			nodeCount = def.NodeGroups[0].Count
		}
		if def.NodeGroups[0].Cloud.Cpu != 0 {
			cpu = def.NodeGroups[0].Cloud.Cpu
		}
		if def.NodeGroups[0].Cloud.Memory != 0 {
			memory = def.NodeGroups[0].Cloud.Memory
		}

		createReq := &capellacontrol.CreateColumnarInstanceRequest{
			Name:        clusterName,
			Description: "",
			Provider:    cloudProvider,
			Region:      cloudRegion,
			Nodes:       nodeCount,
			InstanceTypes: capellacontrol.ColumnarInstanceTypes{
				VCPUs:  fmt.Sprintf("%dvCPUs", cpu),
				Memory: fmt.Sprintf("%dGB", memory),
			},
			Package: capellacontrol.Package{
				Key:      "developerPro",
				Timezone: "PT",
			},
			AvailabilityZone: "single",
		}
		if def.NodeGroups[0].Cloud.ServerImage != "" {
			serverImage := def.NodeGroups[0].Cloud.ServerImage

			releaseId, err := getReleaseIdFromColumnarServerImage(serverImage)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get release id from columnar server image")
			}
			p.logger.Debug("resolved columnar release id", zap.String("releaseId", releaseId))

			createReq.Override = &capellacontrol.CreateOverrideRequest{
				Image:     serverImage,
				Token:     p.overrideToken,
				ReleaseId: releaseId,
			}
			if def.NodeGroups[0].Cloud.ImageAgentHash != "" {
				createReq.Override.Agent = &capellacontrol.CreateOverrideAgentRequest{
					Hash: def.NodeGroups[0].Cloud.ImageAgentHash,
				}
			}
		}
		p.logger.Debug("creating columnar", zap.Any("req", createReq))

		newCluster, err := p.client.CreateColumnar(ctx, p.tenantID, cloudProjectID, createReq)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create columnar")
		}

		cloudClusterID = newCluster.Id

		p.logger.Debug("waiting for creation to complete")

		err = p.mgr.WaitForClusterState(ctx, p.tenantID, cloudClusterID, "healthy", true)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wait for deployment")
		}
	}

	// we cheat for now...
	clusters, err := p.ListClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}

	var thisCluster *ClusterInfo
	for _, cluster := range clusters {
		cluster := cluster.(*ClusterInfo)

		if cluster.ClusterID == clusterID.String() {
			thisCluster = cluster
		}
	}
	if thisCluster == nil {
		return nil, errors.New("failed to find new cluster after deployment")
	}
	return thisCluster, nil
}

func (p *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	var (
		clusterVersion = ""
		serverImage    = ""
		imageAgentHash = ""
	)
	// Ensure all node groups have the same version and image
	for _, nodeGroup := range def.NodeGroups {
		if clusterVersion == "" {
			clusterVersion = nodeGroup.Version
			serverImage = nodeGroup.Cloud.ServerImage
			imageAgentHash = nodeGroup.Cloud.ImageAgentHash
		} else {
			if clusterVersion != nodeGroup.Version || serverImage != nodeGroup.Cloud.ServerImage || imageAgentHash != nodeGroup.Cloud.ImageAgentHash {
				return nil, errors.New("all node groups must have the same version, image and agent hash")
			}
		}
	}

	// Deploy cluster based on presence of server image,
	// specific Columnar images are deployed through the normal createCluster func
	if serverImage != "" && !def.Columnar {
		return p.deployNewCluster(ctx, def, clusterVersion, serverImage)
	} else {
		return p.createNewCluster(ctx, def, clusterVersion)
	}
}

func (d *Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	return nil, errors.New("clouddeploy does not support fetching the cluster definition")
}

func (d *Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	metaData := clusterInfo.Meta
	metaData.Expiry = newExpiryTime
	newProjectName := metaData.String()

	err = d.v4.UpdateProject(
		ctx,
		d.tenantID,
		clusterInfo.ProjectID,
		&capellav4.UpdateProjectRequest{
			Name: newProjectName,
		})
	if err != nil {
		return errors.Wrap(err, "failed to update cluster")
	}

	return nil
}

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	if clusterInfo.Columnar != nil {
		d.logger.Debug("can/will only modify the node count for a columnar cluster")

		if err := d.requireLegacy("columnar cluster modification"); err != nil {
			return err
		}

		newSpec := &capellacontrol.UpdateColumnarInstanceRequest{
			Name:        clusterInfo.Columnar.Name,
			Description: clusterInfo.Columnar.Description,
			Nodes:       def.NodeGroups[0].Count,
		}
		err = d.client.UpdateColumnarSpecs(ctx, d.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, newSpec)
		if err != nil {
			return errors.Wrap(err, "failed to update specs")
		}

		d.logger.Debug("waiting for columnar modification to begin")

		err = d.mgr.WaitForClusterState(ctx, d.tenantID, clusterInfo.Columnar.ID, "scaling", true)
		if err != nil {
			return errors.Wrap(err, "failed to wait for columnar modification to begin")
		}

		d.logger.Debug("waiting for columnar to be healthy")

		err = d.mgr.WaitForClusterState(ctx, d.tenantID, clusterInfo.Columnar.ID, "healthy", true)
		if err != nil {
			return errors.Wrap(err, "failed to wait for columnar to be healthy")
		}

		return nil
	}

	cloudProjectID := clusterInfo.ProjectID
	cloudClusterID := clusterInfo.Cluster.ID
	cloudProvider := clusterInfo.Cluster.CloudProvider.Type

	newGroups, err := buildServiceGroups(cloudProvider, def.NodeGroups)
	if err != nil {
		return errors.Wrap(err, "failed to build cluster specs")
	}

	if !serviceGroupsEqual(cloudProvider, clusterInfo.Cluster.ServiceGroups, newGroups) {
		d.logger.Info("cluster current spec is different from the def spec")
		d.logger.Debug("generated new specification list", zap.Any("specs", newGroups))
		err = d.v4.UpdateCluster(
			ctx,
			d.tenantID,
			cloudProjectID,
			cloudClusterID,
			&capellav4.UpdateClusterRequest{
				Name:          clusterInfo.Cluster.Name,
				Description:   clusterInfo.Cluster.Description,
				Support:       clusterInfo.Cluster.Support,
				ServiceGroups: newGroups,
			})
		if err != nil {
			return errors.Wrap(err, "failed to update cluster specs")
		}

		d.logger.Debug("waiting for cluster modification to begin")

		err = d.v4mgr.WaitForClusterState(ctx, d.tenantID, cloudProjectID, cloudClusterID, capellav4.StateScaling)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster modification to begin")
		}

		d.logger.Debug("waiting for cluster to be healthy")

		err = d.v4mgr.WaitForClusterState(ctx, d.tenantID, cloudProjectID, cloudClusterID, capellav4.StateHealthy)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster to be healthy")
		}
	}

	var (
		clusterVersion = ""
		serverImage    = ""
		releaseId      = ""
	)
	for _, nodeGroup := range def.NodeGroups {
		if clusterVersion == "" {
			clusterVersion = nodeGroup.Version
			serverImage = nodeGroup.Cloud.ServerImage
		} else {
			if clusterVersion != nodeGroup.Version || serverImage != nodeGroup.Cloud.ServerImage {
				return errors.New("all node groups must have the same version and image")
			}
		}
	}

	// The v4 update body has no server version field, so a version change is only
	// possible through the v2 image override.
	if clusterVersion != clusterInfo.Cluster.CouchbaseServer.Version && serverImage != "" {
		if err := d.requireLegacy("server version change"); err != nil {
			return err
		}

		releaseId, err = getReleaseIdFromServerImage(serverImage)
		if err != nil {
			return errors.Wrap(err, "failed to get release id from server image")
		}

		d.logger.Info(fmt.Sprintf("Release id is: %s", releaseId))

		err = d.client.UpdateServerVersion(ctx, d.tenantID, cloudProjectID, cloudClusterID, &capellacontrol.UpdateServerVersionRequest{
			OverrideToken: d.overrideToken,
			ServerImage:   serverImage,
			ServerVersion: clusterVersion,
			ReleaseId:     releaseId,
		})

		if err != nil {
			return errors.Wrap(err, "failed to update server version")
		}

		err = d.v4mgr.WaitForClusterState(ctx, d.tenantID, cloudProjectID, cloudClusterID, capellav4.StateUpgrading)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster upgrade to begin")
		}

		err = d.v4mgr.WaitForClusterState(ctx, d.tenantID, cloudProjectID, cloudClusterID, capellav4.StateHealthy)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster returns to healthy")
		}
	}

	return nil
}

// UpgradeCluster schedules an image upgrade through the internal support
// endpoints, which the v4 API does not expose.
func (d *Deployer) UpgradeCluster(ctx context.Context, clusterID string, CurrentImages string, NewImage string) error {
	if err := d.requireLegacy("cluster image upgrade"); err != nil {
		return err
	}

	clusterInfo, err := d.getCluster(ctx, clusterID)

	if err != nil {
		return err
	}

	var (
		instanceId    = ""
		clusterId     = ""
		cloudProvider = ""
		columnar      = false
	)

	if clusterInfo.Columnar != nil {
		detail, err := d.columnarV2Detail(ctx, clusterInfo)
		if err != nil {
			return err
		}

		instanceId = clusterInfo.Columnar.ID
		clusterId = detail.Config.Id
		cloudProvider = detail.Config.Provider
		columnar = true
	} else if clusterInfo.Cluster != nil {
		instanceId = clusterInfo.Cluster.ID
		clusterId = clusterInfo.Cluster.ID
		cloudProvider = clusterInfo.Cluster.CloudProvider.Type
	}

	var provider string

	switch cloudProvider {
	case "gcp":
		provider = "hostedGCP"
	case "aws":
		provider = "hostedAWS"
	default:
		return errors.New("invalid cloud provider for setup info")
	}

	images := &capellacontrol.Images{
		CurrentImages: []string{CurrentImages},
		NewImage:      NewImage,
		Provider:      provider,
	}

	config := &capellacontrol.Config{
		Type:       "upgradeClusterImage",
		Visibility: "visible",
		Title:      "Upgrade cluster version",
		Priority:   "Upgrade",
		Images:     *images,
	}

	currTime := time.Now().UTC()

	window := &capellacontrol.Window{
		StartDate: currTime.Add(30 * time.Second).Format(time.RFC3339Nano),
		EndDate:   currTime.Add(1 * time.Hour).Format(time.RFC3339Nano),
	}

	err = d.client.UpgradeCloudServerVersion(ctx, d.internalSupportToken, &capellacontrol.UpgradeServerVersionColumnarRequest{
		Config:     *config,
		ClusterIds: []string{clusterId},
		Window:     *window,
		Scope:      "all",
	})

	if err != nil {
		return errors.Wrap(err, "failed to upgrade server version")
	}

	err = d.mgr.WaitForClusterState(ctx, d.tenantID, instanceId, "upgrading", columnar)
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster upgrade to begin")
	}

	d.logger.Debug("waiting for cluster to be healthy")

	err = d.mgr.WaitForClusterState(ctx, d.tenantID, instanceId, "healthy", columnar)
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster to be healthy")
	}

	return nil
}

func (d *Deployer) AddNode(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("clouddeploy does not support cluster node addition")
}

func (d *Deployer) RemoveNode(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("clouddeploy does not support cluster node removal")
}

func (p *Deployer) removeCluster(ctx context.Context, clusterInfo *clusterInfo) error {
	p.logger.Debug("deleting the cloud cluster", zap.String("cluster-id", clusterInfo.Meta.ID.String()))

	if clusterInfo.Cluster != nil {
		err := p.v4.DeleteCluster(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.v4mgr.WaitForClusterState(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, capellav4.StateDeleted)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster destruction")
		}
	} else if clusterInfo.Columnar != nil {
		// Columnar deletion waits on the underlying cloud cluster, whose ID only
		// the v2 record carries.
		detail, err := p.columnarV2Detail(ctx, clusterInfo)
		if err != nil {
			return err
		}

		err = p.client.DeleteColumnar(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
		if err != nil {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.mgr.WaitForColumnarDeletion(ctx, p.tenantID, clusterInfo.Columnar.ID, detail.Config.Id)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster destruction")
		}
	}

	p.logger.Debug("deleting the cloud project")

	err := p.v4.DeleteProject(ctx, p.tenantID, clusterInfo.ProjectID)
	if err != nil {
		return errors.Wrap(err, "failed to delete project")
	}

	return nil
}

func (p *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	return p.removeCluster(ctx, clusterInfo)
}

type AllowListEntry struct {
	ID      string
	Cidr    string
	Comment string
}

func (p *Deployer) ListAllowListEntries(ctx context.Context, clusterID string) ([]*AllowListEntry, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	entries, err := p.listAllowedCidrs(ctx, clusterInfo)
	if err != nil {
		return nil, err
	}

	var out []*AllowListEntry
	for _, entry := range entries {
		out = append(out, &AllowListEntry{
			ID:      entry.ID,
			Cidr:    entry.Cidr,
			Comment: entry.Comment,
		})
	}

	return out, nil
}

// listAllowedCidrs reads the allow list of either cluster kind. The v4 analytics
// API serves allowed CIDRs, so columnar does not need v2 here.
func (p *Deployer) listAllowedCidrs(ctx context.Context, clusterInfo *clusterInfo) ([]*capellav4.AllowedCidrInfo, error) {
	var entries []*capellav4.AllowedCidrInfo
	var err error

	if clusterInfo.Cluster != nil {
		entries, err = p.v4.ListAllowedCidrs(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
	} else {
		entries, err = p.v4.ListAnalyticsAllowedCidrs(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch allow list entries")
	}

	return entries, nil
}

func (p *Deployer) AddAllowListEntry(ctx context.Context, clusterID string, cidr string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	req := &capellav4.CreateAllowedCidrRequest{Cidr: cidr}
	if clusterInfo.Cluster != nil {
		_, err = p.v4.CreateAllowedCidr(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, req)
	} else {
		_, err = p.v4.CreateAnalyticsAllowedCidr(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, req)
	}

	if err != nil {
		return errors.Wrap(err, "failed to update allow list entries")
	}

	return nil
}

func (p *Deployer) RemoveAllowListEntry(ctx context.Context, clusterID string, cidr string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	entries, err := p.listAllowedCidrs(ctx, clusterInfo)
	if err != nil {
		return err
	}

	foundEntryId := ""
	for _, entry := range entries {
		if entry.Cidr == cidr {
			foundEntryId = entry.ID
		}
	}

	if foundEntryId == "" {
		return errors.New("could not find matching cidr")
	}

	if clusterInfo.Cluster != nil {
		err = p.v4.DeleteAllowedCidr(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, foundEntryId)
	} else {
		err = p.v4.DeleteAnalyticsAllowedCidr(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, foundEntryId)
	}

	if err != nil {
		return errors.Wrap(err, "failed to update allow list entries")
	}

	return nil
}

func (p *Deployer) EnablePrivateEndpoints(ctx context.Context, clusterID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	if clusterInfo.Columnar == nil {
		err = p.v4.EnablePrivateEndpointService(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return errors.Wrap(err, "failed to enable private endpoints")
		}
		err = p.v4mgr.WaitForPrivateEndpointServiceEnabled(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
	} else {
		err = p.v4.EnableAnalyticsPrivateEndpointService(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
		if err != nil {
			return errors.Wrap(err, "failed to enable private endpoints")
		}
		err = p.v4mgr.WaitForAnalyticsPrivateEndpointServiceEnabled(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
	}

	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoints to be enabled")
	}

	return nil
}

func (p *Deployer) DisablePrivateEndpoints(ctx context.Context, clusterID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if clusterInfo.Columnar == nil {
		return p.v4.DisablePrivateEndpointService(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
	}
	return p.v4.DisableAnalyticsPrivateEndpointService(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
}

type PrivateEndpointDetails struct {
	ServiceName string
	PrivateDNS  string
}

func (p *Deployer) GetPrivateEndpointDetails(ctx context.Context, clusterID string) (*PrivateEndpointDetails, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	if clusterInfo.Columnar == nil {
		service, err := p.v4.GetPrivateEndpointService(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch private endpoint link details")
		}

		if !service.Enabled {
			return nil, errors.New("private endpoints are not enabled")
		}

		// The private DNS name is reported with the endpoint list rather than with
		// the service in v4.
		endpoints, err := p.v4.ListPrivateEndpoints(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch private endpoints")
		}

		return &PrivateEndpointDetails{
			ServiceName: service.ServiceName,
			PrivateDNS:  endpoints.PrivateEndpointDNS,
		}, nil
	} else {
		service, err := p.v4.GetAnalyticsPrivateEndpointService(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch private endpoint link details")
		}

		if !service.Enabled {
			return nil, errors.New("private endpoints are not enabled")
		}

		return &PrivateEndpointDetails{
			ServiceName: service.ServiceName,
			PrivateDNS:  service.PrivateDNS,
		}, nil
	}

}

func (p *Deployer) GenPrivateEndpointLinkCommand(ctx context.Context, clusterID string, req *capellav4.EndpointCommandRequest) (string, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return "", err
	}

	if clusterInfo.Columnar == nil {
		cmd, err := p.v4.GetPrivateEndpointCommand(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, req)
		if err != nil {
			return "", errors.Wrap(err, "failed to generate private endpoint link command")
		}
		return cmd.Command, nil
	} else {
		return "", errors.New("private endpoint link command generation is not supported for columnar yet")
	}
}

func (p *Deployer) AcceptPrivateEndpointLink(ctx context.Context, clusterID string, endpointID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	if clusterInfo.Columnar != nil {
		return p.acceptColumnarPrivateEndpointLink(ctx, clusterInfo, endpointID)
	}

	cloudProjectID := clusterInfo.ProjectID
	cloudClusterID := clusterInfo.Cluster.ID
	providerName := clusterInfo.Cluster.CloudProvider.Type

	// in some deployment scenarios, the endpoint-id that the user has is only the
	// first part of the id, and the rest of the id comes from somewhere else, so we
	// list all of the ids, and pick the one that matches.
	endpoints, err := p.v4.ListPrivateEndpoints(ctx, p.tenantID, cloudProjectID, cloudClusterID)
	if err != nil {
		return errors.Wrap(err, "failed to list private endpoint links")
	}

	fullEndpointId := ""
	if providerName == capellav4.ProviderGcp {
		// GCP's private endpoint implementation differs from other providers:
		// The endpoint ID is only generated after accepting the link, unlike
		// AWS/Azure where it's available before acceptance. Therefore, we use
		// the provided endpoint ID directly for GCP.
		fullEndpointId = endpointID
	}

	for _, endpoint := range endpoints.Endpoints {
		if strings.Contains(endpoint.ID, endpointID) {
			fullEndpointId = endpoint.ID
			break
		}
	}

	if fullEndpointId == "" {
		return fmt.Errorf("failed to identify endpoint '%s'", endpointID)
	}

	if providerName != capellav4.ProviderGcp {
		_, err = p.v4mgr.WaitForPrivateEndpoint(ctx, p.tenantID, cloudProjectID, cloudClusterID, fullEndpointId)
		if err != nil {
			return errors.Wrap(err, "failed to wait for private endpoint link")
		}
	}

	err = p.v4.AcceptPrivateEndpoint(ctx, p.tenantID, cloudProjectID, cloudClusterID, fullEndpointId)
	if err != nil {
		return errors.Wrap(err, "failed to accept private endpoint link")
	}

	err = p.v4mgr.WaitForPrivateEndpointState(ctx, p.tenantID, cloudProjectID, cloudClusterID, fullEndpointId, capellav4.PrivateEndpointLinked)
	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoint link to establish")
	}

	return nil
}

func (p *Deployer) acceptColumnarPrivateEndpointLink(ctx context.Context, clusterInfo *clusterInfo, endpointID string) error {
	cloudProjectID := clusterInfo.ProjectID
	columnarID := clusterInfo.Columnar.ID
	providerName := clusterInfo.Columnar.CloudProviderName()

	endpoints, err := p.v4.ListAnalyticsPrivateEndpoints(ctx, p.tenantID, cloudProjectID, columnarID)
	if err != nil {
		return errors.Wrap(err, "failed to list private endpoint links")
	}

	fullEndpointId := ""
	if providerName == capellav4.ProviderGcp {
		fullEndpointId = endpointID
	}

	for _, endpoint := range endpoints {
		if strings.Contains(endpoint.ID, endpointID) {
			fullEndpointId = endpoint.ID
			break
		}
	}

	if fullEndpointId == "" {
		return fmt.Errorf("failed to identify endpoint '%s'", endpointID)
	}

	if providerName != capellav4.ProviderGcp {
		_, err = p.v4mgr.WaitForAnalyticsPrivateEndpoint(ctx, p.tenantID, cloudProjectID, columnarID, fullEndpointId)
		if err != nil {
			return errors.Wrap(err, "failed to wait for private endpoint link")
		}
	}

	err = p.v4.AcceptAnalyticsPrivateEndpoint(ctx, p.tenantID, cloudProjectID, columnarID, fullEndpointId)
	if err != nil {
		return errors.Wrap(err, "failed to accept private endpoint link")
	}

	err = p.v4mgr.WaitForAnalyticsPrivateEndpointState(ctx, p.tenantID, cloudProjectID, columnarID, fullEndpointId, capellav4.PrivateEndpointLinked)
	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoint link to establish")
	}

	return nil
}

// removalTarget is one cluster queued for deletion by RemoveAll. Deletions are
// issued for every target before any wait, so that clusters tear down at the same
// time instead of one after another.
type removalTarget struct {
	projectID string
	clusterID string
	// underlyingID is the cloud cluster behind a columnar instance, which its
	// deletion wait needs.
	underlyingID string
	isColumnar   bool
}

func (p *Deployer) RemoveAll(ctx context.Context) error {
	var errs error

	projects, err := p.listCbdc2Projects(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list projects")
	}

	// Corrupted projects can hold more than one cluster, so the clusters are read
	// directly rather than through inspectProject, which collapses them.
	var targets []removalTarget
	failedProjects := make(map[string]bool)
	for _, project := range projects {
		clusters, err := p.v4.ListClusters(ctx, p.tenantID, project.Info.ID)
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to list clusters"))
			failedProjects[project.Info.ID] = true
			continue
		}

		for _, cluster := range clusters {
			targets = append(targets, removalTarget{
				projectID: project.Info.ID,
				clusterID: cluster.ID,
			})
		}

		columnars, err := p.v4.ListAnalyticsClusters(ctx, p.tenantID, project.Info.ID)
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to list analytics clusters"))
			failedProjects[project.Info.ID] = true
			continue
		}

		for _, columnar := range columnars {
			detail, err := p.columnarV2DetailByID(ctx, columnar.ID)
			if err != nil {
				errs = multierr.Append(errs, err)
				failedProjects[project.Info.ID] = true
				continue
			}

			targets = append(targets, removalTarget{
				projectID:    project.Info.ID,
				clusterID:    columnar.ID,
				underlyingID: detail.Config.Id,
				isColumnar:   true,
			})
		}
	}

	p.logger.Info("found clusters to remove", zap.Int("count", len(targets)))

	for _, target := range targets {
		p.logger.Info("removing a cluster", zap.String("cluster-id", target.clusterID))

		if target.isColumnar {
			err = p.client.DeleteColumnar(ctx, p.tenantID, target.projectID, target.clusterID)
		} else {
			err = p.v4.DeleteCluster(ctx, p.tenantID, target.projectID, target.clusterID)
		}
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to remove cluster"))
			failedProjects[target.projectID] = true
		}
	}

	for _, target := range targets {
		p.logger.Info("waiting for cluster removal to complete", zap.String("cluster-id", target.clusterID))

		if target.isColumnar {
			err = p.mgr.WaitForColumnarDeletion(ctx, p.tenantID, target.clusterID, target.underlyingID)
		} else {
			err = p.v4mgr.WaitForClusterState(ctx, p.tenantID, target.projectID, target.clusterID, capellav4.StateDeleted)
		}
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to wait for cluster to complete"))
			failedProjects[target.projectID] = true
		}
	}

	// A project is only removed once everything inside it is gone, since Capella
	// refuses to delete a project that still holds a cluster.
	for _, project := range projects {
		if failedProjects[project.Info.ID] {
			p.logger.Warn("keeping project as its clusters were not all removed",
				zap.String("project-id", project.Info.ID))
			continue
		}

		p.logger.Info("removing a project", zap.String("project-id", project.Info.ID))

		err := p.v4.DeleteProject(ctx, p.tenantID, project.Info.ID)
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to remove project"))
		}
	}

	if errs != nil {
		return multierr.Combine(errs)
	}

	return nil
}

func (p *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	var connStr string
	var dataApiConnstr string
	var dnsSRV string
	if clusterInfo.Cluster != nil {
		connStr = fmt.Sprintf("couchbases://%s", clusterInfo.Cluster.ConnectionString)
		dnsSRV = clusterInfo.Cluster.ConnectionString

		// The Data API connection string is on a separate resource in v4. A cluster
		// without the Data API enabled must still report its normal connection
		// string, so a failure here is not fatal.
		dataApi, err := p.v4.GetDataApi(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			p.logger.Debug("failed to fetch data api details", zap.Error(err))
		} else if dataApi.ConnectionString != "" {
			dataApiConnstr = fmt.Sprintf("https://%s", dataApi.ConnectionString)
		}
	} else {
		// The v4 analytics API reports no connection string, so this needs v2.
		detail, err := p.columnarV2Detail(ctx, clusterInfo)
		if err != nil {
			return nil, err
		}

		connStr = fmt.Sprintf("couchbases://%s", detail.Config.Endpoint)
		dnsSRV = detail.Config.Endpoint
	}

	return &deployment.ConnectInfo{
		ConnStr:        "",
		ConnStrTls:     connStr,
		Mgmt:           "",
		MgmtTls:        "",
		DataApiConnstr: dataApiConnstr,
		DnsSRVName:     dnsSRV,
	}, nil
}

func (p *Deployer) Cleanup(ctx context.Context) error {
	// we just use our own commands to do this easily...
	clusters, err := p.listClusters(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list clusters")
	}

	curTime := time.Now()
	var allErr error
	for _, cluster := range clusters {
		// A project with nothing in it is left behind by a cluster that was already
		// removed, or by a create that failed part way. v4 projects report no cluster
		// count, so emptiness comes from the listing above.
		if cluster.Cluster == nil && cluster.Columnar == nil && !cluster.IsCorrupted {
			p.logger.Info("removing empty project",
				zap.String("project-id", cluster.ProjectID))

			err := p.v4.DeleteProject(ctx, p.tenantID, cluster.ProjectID)
			if err != nil {
				allErr = multierr.Append(allErr, errors.Wrapf(err, "project_id: %s", cluster.ProjectID))
			}
			continue
		}

		if !cluster.Meta.Expiry.IsZero() && !cluster.Meta.Expiry.After(curTime) {
			p.logger.Info("removing cluster",
				zap.String("cluster-id", cluster.Meta.ID.String()))

			if cluster.Cluster != nil && cluster.Cluster.CurrentState == "destroy_failed" {
				p.logger.Warn("skipping due to destroy_failed state (cluster)")
				continue
			}
			if cluster.Columnar != nil && cluster.Columnar.CurrentState == "destroy_failed" {
				p.logger.Warn("skipping due to destroy_failed state (columnar)")
				continue
			}

			err := p.removeCluster(ctx, cluster)
			if err != nil {
				allErr = multierr.Append(allErr, errors.Wrapf(err, "cluster_id: %s", cluster.Meta.ID.String()))
			}
		}
	}

	if allErr != nil {
		return multierr.Combine(allErr)
	}

	return nil
}

func (p *Deployer) ListUsers(ctx context.Context, clusterID string) ([]deployment.UserInfo, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	if clusterInfo.Cluster != nil {
		resp, err := p.v4.ListUsers(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to list users")
		}

		var users []deployment.UserInfo
		for _, user := range resp {
			users = append(users, deployment.UserInfo{
				Username: user.Name,
				CanRead:  user.HasPrivilege(capellav4.PrivilegeDataReader),
				CanWrite: user.HasPrivilege(capellav4.PrivilegeDataWriter),
			})
		}

		return users, nil
	} else {
		if err := p.requireLegacy("columnar database credentials"); err != nil {
			return nil, err
		}

		resp, err := p.mgr.Client.ListColumnarUsers(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       1000,
			SortBy:        "name",
			SortDirection: "asc",
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to list users")
		}

		var users []deployment.UserInfo
		for _, user := range resp.Data {
			canRead := user.Permissions.Read.Accessible
			canWrite := user.Permissions.Create.Accessible

			users = append(users, deployment.UserInfo{
				Username: user.Data.Name,
				CanRead:  canRead,
				CanWrite: canWrite,
			})
		}

		return users, nil
	}

}

func (p *Deployer) CreateUser(ctx context.Context, clusterID string, opts *deployment.CreateUserOptions) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if clusterInfo.Cluster != nil {
		var privileges []string
		if opts.CanRead {
			privileges = append(privileges, capellav4.PrivilegeDataReader)
		}
		if opts.CanWrite {
			privileges = append(privileges, capellav4.PrivilegeDataWriter)
		}

		_, err = p.v4.CreateUser(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, &capellav4.CreateUserRequest{
			Name:     opts.Username,
			Password: opts.Password,
			Access: []capellav4.UserAccess{
				{Privileges: privileges},
			},
		})
		if err != nil {
			return errors.Wrap(err, "failed to create user")
		}
	} else {
		if err := p.requireLegacy("columnar database credentials"); err != nil {
			return err
		}

		roles, err := p.mgr.Client.GetColumnarRoles(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       250,
			SortBy:        "name",
			SortDirection: "asc",
		})
		if err != nil {
			return errors.Wrap(err, "failed to get default roles")
		}

		var roleIds []string
		for _, role := range roles.Data {
			roleIds = append(roleIds, role.Data.ID)
		}

		err = p.mgr.Client.CreateColumnarUser(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.CreateColumnarUserRequest{
			Name:     opts.Username,
			Password: opts.Password,
			Roles:    roleIds,
		})

		if err != nil {
			return errors.Wrap(err, "failed to create user")
		}
	}

	return nil
}

func (p *Deployer) DeleteUser(ctx context.Context, clusterID string, username string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if clusterInfo.Cluster != nil {
		resp, err := p.v4.ListUsers(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return errors.Wrap(err, "failed to list users")
		}

		userId := ""
		for _, user := range resp {
			if user.Name == username {
				userId = user.ID
				break
			}
		}
		if userId == "" {
			return errors.New("failed to find user by username")
		}

		err = p.v4.DeleteUser(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, userId)
		if err != nil {
			return errors.Wrap(err, "failed to delete user")
		}

		return nil
	} else {
		if err := p.requireLegacy("columnar database credentials"); err != nil {
			return err
		}

		resp, err := p.mgr.Client.ListColumnarUsers(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       1000,
			SortBy:        "name",
			SortDirection: "asc",
		})
		if err != nil {
			return errors.Wrap(err, "failed to list users")
		}
		userId := ""
		for _, user := range resp.Data {
			if user.Data.Name == username {
				userId = user.Data.ID
				break
			}
		}
		if userId == "" {
			return errors.New("failed to find user by username")
		}

		err = p.mgr.Client.DeleteColumnarUser(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, userId)
		if err != nil {
			return errors.Wrap(err, "failed to delete user")
		}

		return nil
	}

}

func (p *Deployer) ListBuckets(ctx context.Context, clusterID string) ([]deployment.BucketInfo, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	resp, err := p.v4.ListBuckets(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list buckets")
	}

	var buckets []deployment.BucketInfo
	for _, bucket := range resp {
		buckets = append(buckets, deployment.BucketInfo{
			Name: bucket.Name,
		})
	}

	return buckets, nil
}

func (p *Deployer) CreateBucket(ctx context.Context, clusterID string, opts *deployment.CreateBucketOptions) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	ramQuotaMb := 256
	if opts.RamQuotaMB > 0 {
		ramQuotaMb = opts.RamQuotaMB
	}

	numReplicas := 1
	if opts.NumReplicas > 1 {
		numReplicas = opts.NumReplicas
	}

	capellaBucketType, storageBackend, err := capellaBucketParams(opts.BucketType)
	if err != nil {
		return err
	}

	_, err = p.v4.CreateBucket(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, &capellav4.CreateBucketRequest{
		BucketConflictResolution: "seqno",
		DurabilityLevel:          "none",
		Flush:                    opts.FlushEnabled,
		MemoryAllocationInMb:     ramQuotaMb,
		Name:                     opts.Name,
		Replicas:                 numReplicas,
		StorageBackend:           storageBackend,
		Type:                     capellaBucketType,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create bucket")
	}

	return nil
}

// capellaBucketParams maps a deployment bucket type onto the Capella bucket
// "type" and storage backend values. Memcached buckets are not offered by
// Capella, so they are rejected explicitly rather than sent as an invalid
// request. An empty bucket type defaults to couchbase.
func capellaBucketParams(bucketType deployment.BucketType) (capellaType string, storageBackend string, err error) {
	if bucketType == "" {
		bucketType = deployment.BucketTypeCouchbase
	}

	switch bucketType {
	case deployment.BucketTypeCouchbase:
		return "couchbase", "couchstore", nil
	case deployment.BucketTypeEphemeral:
		// Ephemeral buckets are memory-only and reject a disk storage
		// backend, so leave it empty (omitted from the request).
		return "ephemeral", "", nil
	case deployment.BucketTypeMemcached:
		return "", "", errors.New("memcached buckets are not supported by the cloud deployer")
	default:
		return "", "", errors.Errorf("unsupported bucket type %q", bucketType)
	}
}

func (p *Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	// we can infer the bucket id by name right now
	bucketId := base64.StdEncoding.EncodeToString([]byte(bucketName))

	err = p.v4.DeleteBucket(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, bucketId)
	if err != nil {
		return errors.Wrap(err, "failed to delete bucket")
	}

	return nil
}

func (d *Deployer) LoadSampleBucket(ctx context.Context, clusterID string, bucketName string) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	if clusterInfo.Columnar == nil {
		_, err := d.v4.LoadSampleBucket(ctx, d.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID,
			&capellav4.LoadSampleBucketRequest{Name: bucketName})
		return err
	}

	if err := d.requireLegacy("columnar sample buckets"); err != nil {
		return err
	}

	req := &capellacontrol.LoadColumnarSampleBucketRequest{SampleName: bucketName}
	return d.mgr.Client.LoadColumnarSampleBucket(ctx, d.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID, req)
}

func (p *Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return "", err
	}

	if clusterInfo.Cluster != nil {
		cert, err := p.v4.GetCertificate(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return "", errors.Wrap(err, "failed to get trusted CAs")
		}
		return strings.TrimSpace(cert), nil
	}

	// The v4 analytics API serves no certificates.
	if err := p.requireLegacy("columnar certificates"); err != nil {
		return "", err
	}

	resp, err := p.mgr.Client.GetTrustedCAsColumnar(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get trusted CAs")
	}

	var returnCert capellacontrol.GetTrustedCAsResponse_Certificate
	for _, cert := range *resp {
		if strings.Contains(cert.Subject, "O=Couchbase, OU=Cloud") {
			returnCert = cert
			break
		}
	}

	return strings.TrimSpace(returnCert.Pem), nil
}

func (d *Deployer) GetMetrics(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("clouddeploy does not support getting required metrics as of now. Refer - AV-118082")
}

func (d *Deployer) startLogCollection(ctx context.Context, cloudClusterId string) error {
	var startCollectingServerLogsRequest = &capellacontrol.StartCollectingServerLogsRequest{
		HostName: d.uploadServerLogsHostName,
	}

	var err = d.mgr.Client.StartCollectingServerLogs(ctx, cloudClusterId, d.internalSupportToken,
		startCollectingServerLogsRequest)

	if err != nil {
		errors.Wrap(err,
			fmt.Sprintf("failed to start server log collection: %s", err))
	} else {
		d.logger.Info(fmt.Sprintf("Log collection have started for cluster: %s", cloudClusterId))
	}

	return err
}

// CollectLogs gathers server logs through the internal support endpoints, which
// the v4 API does not expose.
func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	if strings.TrimSpace(d.uploadServerLogsHostName) == "" {
		return nil, fmt.Errorf("cannot collect server logs: no upload-server-logs host name is configured; " +
			"set it via `cbdinocluster init` (--upload-server-logs-host-name) or Capella.UploadServerLogsHostName in your config")
	}

	if err := d.requireLegacy("server log collection"); err != nil {
		return nil, err
	}

	cluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return []string{}, err
	}

	var cloudClusterId string
	if cluster.Columnar != nil {
		detail, err := d.columnarV2Detail(ctx, cluster)
		if err != nil {
			return nil, err
		}
		cloudClusterId = detail.Config.Id
	} else if cluster.Cluster != nil {
		cloudClusterId = cluster.Cluster.ID
	}

	err = d.startLogCollection(ctx, cloudClusterId)

	if err != nil {
		return nil, err
	}

	var downloadServerLogsRequest = &capellacontrol.DownloadServerLogsRequest{
		HostName: d.uploadServerLogsHostName,
	}

	perNodeMap, err := d.mgr.WaitForServerLogsCollected(ctx, cloudClusterId, d.internalSupportToken,
		downloadServerLogsRequest)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for logs to be collected")
	}

	var downloadedPaths []string
	for node, logInfo := range perNodeMap {
		if logInfo.Url == "" {
			continue
		}

		logFileName := fmt.Sprintf("%s_logs", node)
		logFilePath := filepath.Join(destPath, logFileName)
		d.logger.Info(fmt.Sprintf("Downloading logs for %s", node))
		err := webhelper.DownloadFileFromURL(logInfo.Url, logFilePath)
		if err != nil {
			d.logger.Info(fmt.Sprintf("Error downloading logs for %s: %v", node, err))
			continue
		}

		d.logger.Info(fmt.Sprintf("Logs for %s downloaded successfully.", node))
		downloadedPaths = append(downloadedPaths, logFilePath)
	}

	return downloadedPaths, nil
}

// RedeployCluster uses an internal support endpoint that the v4 API does not
// expose.
func (d *Deployer) RedeployCluster(ctx context.Context, clusterID string) error {
	if err := d.requireLegacy("cluster redeploy"); err != nil {
		return err
	}

	cluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if cluster.Columnar != nil {
		return errors.New("redeploy not supported for columanr clusters yet")
	}

	err = d.mgr.Client.RedeployCluster(ctx, cluster.Cluster.ID, d.internalSupportToken)

	if err != nil {
		errors.Wrap(err, "Failed to redeploy cluster")
	}

	d.logger.Debug("waiting for redeploy cluster to begin")

	err = d.v4mgr.WaitForClusterState(ctx, d.tenantID, cluster.ProjectID, cluster.Cluster.ID, capellav4.StateRebalancing)
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster modification to begin")
	}

	d.logger.Debug("waiting for cluster to be healthy")

	err = d.v4mgr.WaitForClusterState(ctx, d.tenantID, cluster.ProjectID, cluster.Cluster.ID, capellav4.StateHealthy)
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster to be healthy")
	}

	return nil
}

// CreateCapellaLink and the other link operations run analytics links, which the
// v4 analytics API does not cover.
func (d *Deployer) CreateCapellaLink(ctx context.Context, columnarID, linkName, clusterId, directID string) error {
	columnarInfo, err := d.getCluster(ctx, columnarID)
	if err != nil {
		return err
	}
	if columnarInfo.Columnar == nil {
		return errors.Wrap(err, "this is not a columnar cluster")
	}
	if err := d.requireLegacy("columnar links"); err != nil {
		return err
	}

	resolvedClusterId := directID
	if directID == "" {
		clusterInfo, err := d.getCluster(ctx, clusterId)
		if err != nil {
			return err
		}
		if clusterInfo.Columnar != nil {
			return errors.Wrap(err, "can not link to another columnar cluster")
		}
		resolvedClusterId = clusterInfo.Cluster.ID
	}

	req := &capellacontrol.CreateColumnarCapellaLinkRequest{
		LinkName:           linkName,
		ProvisionedCluster: capellacontrol.ProvisionedCluster{ClusterId: resolvedClusterId},
	}
	return d.mgr.Client.CreateColumnarCapellaLink(ctx, d.tenantID, columnarInfo.ProjectID, columnarInfo.Columnar.ID, req)
}

func (d *Deployer) CreateS3Link(ctx context.Context, columnarID, linkName, region, endpoint, accessKey, secretKey string) error {
	columnarInfo, err := d.getCluster(ctx, columnarID)
	if err != nil {
		return err
	}
	if columnarInfo.Columnar == nil {
		return errors.Wrap(err, "this is not a columnar cluster")
	}
	if err := d.requireLegacy("columnar links"); err != nil {
		return err
	}

	req := &capellacontrol.CreateColumnarS3LinkRequest{
		Region:          region,
		AccessKeyId:     accessKey,
		SecretAccessKey: secretKey,
		SessionToken:    "",
		Endpoint:        endpoint,
		Type:            "s3",
	}
	return d.mgr.Client.CreateColumnarS3Link(ctx, d.tenantID, columnarInfo.ProjectID, columnarInfo.Columnar.ID, linkName, req)
}

func (d *Deployer) DropLink(ctx context.Context, columnarID, linkName string) error {
	columnarInfo, err := d.getCluster(ctx, columnarID)
	if err != nil {
		return err
	}
	if columnarInfo.Columnar == nil {
		return errors.Wrap(err, "this is not a columnar cluster")
	}
	if err := d.requireLegacy("columnar links"); err != nil {
		return err
	}

	req := &capellacontrol.ColumnarQueryRequest{
		Statement:   fmt.Sprintf("DROP LINK `%s`", linkName),
		MaxWarnings: 25,
	}
	return d.mgr.Client.DoBasicColumnarQuery(ctx, d.tenantID, columnarInfo.ProjectID, columnarInfo.Columnar.ID, req)
}

func (d *Deployer) EnableDataApi(ctx context.Context, clusterID string) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	cloudProjectID := clusterInfo.ProjectID
	cloudClusterID := clusterInfo.Cluster.ID

	d.logger.Debug("enabling data API")

	err = d.v4.UpdateDataApi(ctx, d.tenantID, cloudProjectID, cloudClusterID, &capellav4.UpdateDataApiRequest{
		EnableDataApi: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to enable Data API")
	}

	d.logger.Debug("waiting for Data API to enable")

	err = d.v4mgr.WaitForDataApiEnabled(ctx, d.tenantID, cloudProjectID, cloudClusterID)
	if err != nil {
		return errors.Wrap(err, "failed to wait for Data API enablement")
	}

	return nil
}

func (d *Deployer) GetGatewayCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("clouddeploy does not support getting gateway certificates")
}

// getQueryX builds a query client that runs through the v2 query passthrough
// proxy. The v4 API has no equivalent.
func (d *Deployer) getQueryX(ctx context.Context, clusterID string) (*cbqueryx.Query, error) {
	if err := d.requireLegacy("running queries"); err != nil {
		return nil, err
	}

	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	qcli, err := d.client.GetQueryX(ctx, d.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
	if err != nil {
		return nil, err
	}

	return qcli, nil
}

// bucketTarget resolves the identifiers that the v4 scope and collection
// endpoints need. Capella derives a bucket ID from its name, so no lookup is
// required.
func (d *Deployer) bucketTarget(ctx context.Context, clusterID string, bucketName string) (projectID string, cloudClusterID string, bucketID string, err error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return "", "", "", err
	}
	if clusterInfo.Cluster == nil {
		return "", "", "", errors.New("buckets are not supported for columnar clusters")
	}

	return clusterInfo.ProjectID,
		clusterInfo.Cluster.ID,
		base64.StdEncoding.EncodeToString([]byte(bucketName)),
		nil
}

func (d *Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error) {
	qcli, err := d.getQueryX(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get query client")
	}

	results, err := qcli.Query(ctx, &cbqueryx.QueryOptions{
		Statement: query,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to execute query")
	}

	rows := make([]json.RawMessage, 0)
	for results.HasMoreRows() {
		row, err := results.ReadRow()
		if err != nil {
			return "", errors.Wrap(err, "failed to read row")
		}

		rows = append(rows, row)
	}

	rowsBytes, err := json.Marshal(rows)
	if err != nil {
		return "", errors.Wrap(err, "failed to serialize rows")
	}

	return string(rowsBytes), nil
}

func (d *Deployer) ListCollections(ctx context.Context, clusterID string, bucketName string) ([]deployment.ScopeInfo, error) {
	projectID, cloudClusterID, bucketID, err := d.bucketTarget(ctx, clusterID, bucketName)
	if err != nil {
		return nil, err
	}

	resp, err := d.v4.ListScopes(ctx, d.tenantID, projectID, cloudClusterID, bucketID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch collection manifest")
	}

	var scopes []deployment.ScopeInfo
	for _, scope := range resp {
		var collections []deployment.CollectionInfo
		for _, collection := range scope.Collections {
			collections = append(collections, deployment.CollectionInfo{
				Name: collection.Name,
			})
		}
		scopes = append(scopes, deployment.ScopeInfo{
			Name:        scope.Name,
			Collections: collections,
		})
	}

	return scopes, nil
}

func (d *Deployer) CreateScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	projectID, cloudClusterID, bucketID, err := d.bucketTarget(ctx, clusterID, bucketName)
	if err != nil {
		return err
	}

	err = d.v4.CreateScope(ctx, d.tenantID, projectID, cloudClusterID, bucketID, &capellav4.CreateScopeRequest{
		Name: scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create scope")
	}

	return nil
}

func (d *Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	projectID, cloudClusterID, bucketID, err := d.bucketTarget(ctx, clusterID, bucketName)
	if err != nil {
		return err
	}

	err = d.v4.CreateCollection(ctx, d.tenantID, projectID, cloudClusterID, bucketID, scopeName, &capellav4.CreateCollectionRequest{
		Name: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create collection")
	}

	return nil
}

func (d *Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	projectID, cloudClusterID, bucketID, err := d.bucketTarget(ctx, clusterID, bucketName)
	if err != nil {
		return err
	}

	err = d.v4.DeleteScope(ctx, d.tenantID, projectID, cloudClusterID, bucketID, scopeName)
	if err != nil {
		return errors.Wrap(err, "failed to delete scope")
	}

	return nil
}

func (d *Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	projectID, cloudClusterID, bucketID, err := d.bucketTarget(ctx, clusterID, bucketName)
	if err != nil {
		return err
	}

	err = d.v4.DeleteCollection(ctx, d.tenantID, projectID, cloudClusterID, bucketID, scopeName, collectionName)
	if err != nil {
		return errors.Wrap(err, "failed to delete collection")
	}

	return nil
}

func (d *Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string, trafficType deployment.BlockNodeTrafficType, rejectType string) error {
	return errors.New("clouddeploy does not support traffic control")
}

func (d *Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("clouddeploy does not support traffic control")
}

func (d *Deployer) PartitionNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string, rejectType string) error {
	return errors.New("clouddeploy does not support traffic control")
}

func (d *Deployer) ListImages(ctx context.Context) ([]deployment.Image, error) {
	return nil, errors.New("clouddeploy does not support image listing")
}

func (d *Deployer) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	return nil, errors.New("clouddeploy does not support image search")
}

func (d *Deployer) PauseNode(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("clouddeploy does not support node pausing")
}

func (d *Deployer) UnpauseNode(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("clouddeploy does not support node pausing")
}

func (d *Deployer) FailOverNode(ctx context.Context, clusterID string, nodeID string, failOverType deployment.FailOverType, allowUnsafe bool) error {
	return errors.New("clouddeploy does not support failing over a node")
}

func (d *Deployer) SetNodeRecovery(ctx context.Context, clusterID string, nodeID string, recoverType deployment.RecoveryType) error {
	return errors.New("clouddeploy does not support failover recovery")
}

func (d *Deployer) RebalanceCluster(ctx context.Context, clusterID string, nodesToEject []string) error {
	return errors.New("clouddeploy does not support rebalance cluster")
}

func (d *Deployer) KillCouchbase(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("clouddeploy does not support killing couchbase process")
}

func (d *Deployer) SetAutoFailover(ctx context.Context, clusterID string, enabled bool, timeout int) error {
	return errors.New("clouddeploy does not support setting auto-failover")
}
