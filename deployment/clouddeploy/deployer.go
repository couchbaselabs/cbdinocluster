package clouddeploy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/couchbase/gocbcorex"
	"github.com/couchbaselabs/cbdinocluster/utils/webhelper"
	"github.com/couchbaselabs/gocbconnstr/v2"
	"go.uber.org/multierr"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/deployment/commondeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// The internal v2 API is used only where v4 has no equivalent. Authentication
// there invalidates any other active session for the same user.
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
	projectID                string
}

var _ deployment.Deployer = (*Deployer)(nil)
var _ deployment.ScopedRemoveAller = (*Deployer)(nil)
var _ deployment.ScopedCleaner = (*Deployer)(nil)

type NewDeployerOptions struct {
	Logger                   *zap.Logger
	Client                   *capellacontrol.Controller
	V4Client                 *capellav4.Client
	HasLegacyCredentials     bool
	TenantID                 string
	OverrideToken            string
	InternalSupportToken     string
	DefaultCloud             string
	DefaultAwsRegion         string
	DefaultAzureRegion       string
	DefaultGcpRegion         string
	UploadServerLogsHostName string
	ProjectID                string
}

func NewDeployer(opts *NewDeployerOptions) (*Deployer, error) {
	if opts.V4Client == nil {
		return nil, errors.New("a capella v4 client is required")
	}

	// Capella lists project IDs in lower case, and the deployer compares them
	// with that list.
	projectID := strings.ToLower(opts.ProjectID)

	// Empty is valid. Reads and removal of old layout clusters need no project.
	if projectID != "" {
		if err := CheckProjectID(projectID); err != nil {
			return nil, err
		}
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
		projectID:                projectID,
	}, nil
}

func (p *Deployer) requireLegacy(feature string) error {
	if p.hasLegacyCredentials {
		return nil
	}
	return errors.Errorf("%s needs the internal capella v2 api, which requires a "+
		"username and password; note that authenticating there invalidates other "+
		"active sessions for the same user", feature)
}

// The support token authenticates by itself and creates no v2 session.
func (p *Deployer) requireSupportToken(feature string) error {
	if p.internalSupportToken != "" {
		return nil
	}
	return errors.Errorf("%s needs the capella internal support token; set it "+
		"with `cbdinocluster init` or CAPELLA_INTERNAL_SUPPORT_TOKEN", feature)
}

// In the old layout each cluster has its own project, and the project name
// carries the cluster meta data. In the shared layout all clusters live in one
// project, and each cluster name carries its own meta data.
type clusterInfo struct {
	Meta        *stringclustermeta.MetaData
	ProjectID   string
	ProjectName string
	Cluster     *capellav4.ClusterInfo
	Columnar    *capellav4.AnalyticsClusterInfo
	Legacy      bool
	IsCorrupted bool
}

type cbdc2Project struct {
	Meta *stringclustermeta.MetaData
	Info *capellav4.ProjectInfo
}

const maxProjectInspectConcurrency = 8

// listProjects returns the configured project, nil when no project ID is set,
// and the cbdc2 projects of the old layout. It lists the projects only once.
func (p *Deployer) listProjects(ctx context.Context) (*capellav4.ProjectInfo, []cbdc2Project, error) {
	p.logger.Debug("listing cloud projects")

	projects, err := p.v4.ListProjects(ctx, p.tenantID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to list projects")
	}

	shared, legacy := splitProjects(projects, p.projectID, p.logger)
	return shared, legacy, nil
}

// splitProjects never treats the configured project as an old layout project,
// even when its name parses as cbdc2 meta data. The configured project keeps
// an empty name when the list does not hold it.
func splitProjects(
	projects []*capellav4.ProjectInfo,
	sharedID string,
	logger *zap.Logger,
) (*capellav4.ProjectInfo, []cbdc2Project) {
	var shared *capellav4.ProjectInfo
	if sharedID != "" {
		shared = &capellav4.ProjectInfo{ID: sharedID}
	}

	var legacy []cbdc2Project
	for _, project := range projects {
		if sharedID != "" && project.ID == sharedID {
			shared = project
			continue
		}

		meta, err := stringclustermeta.Parse(project.Name)
		if err != nil {
			// One malformed name in the shared org must not block the other
			// projects, in particular during cleanup and remove-all.
			logger.Warn("failed to parse meta-data from project name, skipping project",
				zap.String("project-name", project.Name),
				zap.Error(err))
			continue
		}
		if meta == nil {
			continue
		}

		legacy = append(legacy, cbdc2Project{Meta: meta, Info: project})
	}

	return shared, legacy
}

func (p *Deployer) inspectProject(ctx context.Context, project cbdc2Project) (*clusterInfo, error) {
	projectID := project.Info.ID

	base := &clusterInfo{
		Meta:        project.Meta,
		ProjectID:   projectID,
		ProjectName: project.Info.Name,
		Legacy:      true,
	}

	clusters, err := p.v4.ListClusters(ctx, p.tenantID, projectID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters for project")
	}

	// The org wide analytics listing carries no project, so ask per project.
	projectColumnars, err := p.v4.ListAnalyticsClusters(ctx, p.tenantID, projectID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list analytics clusters for project")
	}

	if len(clusters)+len(projectColumnars) > 1 {
		base.IsCorrupted = true
		return base, nil
	}
	if len(clusters) == 1 {
		base.Cluster = clusters[0]
	} else if len(projectColumnars) == 1 {
		base.Columnar = projectColumnars[0]
	}

	return base, nil
}

// listSharedClusters returns the cbdinocluster clusters of the configured
// project. A missing project gives no clusters, so ps and cleanup still handle
// the old layout.
func (p *Deployer) listSharedClusters(ctx context.Context, project *capellav4.ProjectInfo) ([]*clusterInfo, error) {
	clusters, err := p.v4.ListClusters(ctx, p.tenantID, project.ID)
	if err != nil {
		if capellav4.IsProjectNotFound(err) {
			p.logSharedProjectNotFound(project.ID)
			return nil, nil
		}
		return nil, errors.Wrap(err, "failed to list clusters for the shared project")
	}

	columnars, err := p.v4.ListAnalyticsClusters(ctx, p.tenantID, project.ID)
	if err != nil {
		if capellav4.IsProjectNotFound(err) {
			p.logSharedProjectNotFound(project.ID)
			return nil, nil
		}
		return nil, errors.Wrap(err, "failed to list analytics clusters for the shared project")
	}

	return sharedClusterInfos(project, clusters, columnars, p.logger), nil
}

func (p *Deployer) logSharedProjectNotFound(projectID string) {
	p.logger.Warn("the configured capella project does not exist, skipping its clusters",
		zap.String("project-id", projectID))
}

// sharedClusterInfos keeps only the clusters whose name parses as meta data.
// Other users can own clusters in the shared project too.
func sharedClusterInfos(
	project *capellav4.ProjectInfo,
	clusters []*capellav4.ClusterInfo,
	columnars []*capellav4.AnalyticsClusterInfo,
	logger *zap.Logger,
) []*clusterInfo {
	var out []*clusterInfo
	for _, cluster := range clusters {
		meta := parseClusterNameMeta(cluster.Name, logger)
		if meta == nil {
			continue
		}
		out = append(out, &clusterInfo{
			Meta:        meta,
			ProjectID:   project.ID,
			ProjectName: project.Name,
			Cluster:     cluster,
		})
	}
	for _, columnar := range columnars {
		meta := parseClusterNameMeta(columnar.Name, logger)
		if meta == nil {
			continue
		}
		out = append(out, &clusterInfo{
			Meta:        meta,
			ProjectID:   project.ID,
			ProjectName: project.Name,
			Columnar:    columnar,
		})
	}
	return out
}

func parseClusterNameMeta(name string, logger *zap.Logger) *stringclustermeta.MetaData {
	meta, err := stringclustermeta.Parse(name)
	if err != nil {
		logger.Warn("failed to parse meta-data from cluster name, skipping cluster",
			zap.String("cluster-name", name),
			zap.Error(err))
		return nil
	}
	return meta
}

// findClusters inspects only the cbdc2 projects whose cluster ID matches, so a
// single lookup costs one project listing instead of one per project. It lists
// clusters only in those projects and in the shared project.
func (p *Deployer) findClusters(ctx context.Context, idPrefix string) ([]*clusterInfo, error) {
	shared, projects, err := p.listProjects(ctx)
	if err != nil {
		return nil, err
	}

	var matched []cbdc2Project
	for _, project := range projects {
		if strings.HasPrefix(project.Meta.ID.String(), idPrefix) {
			matched = append(matched, project)
		}
	}

	var sharedMatched []*clusterInfo
	if shared != nil {
		sharedClusters, err := p.listSharedClusters(ctx, shared)
		if err != nil {
			return nil, err
		}

		for _, cluster := range sharedClusters {
			if strings.HasPrefix(cluster.Meta.ID.String(), idPrefix) {
				sharedMatched = append(sharedMatched, cluster)
			}
		}
	}

	p.logger.Debug("listing cloud clusters",
		zap.Int("projects", len(projects)),
		zap.Int("matched-projects", len(matched)),
		zap.Int("matched-shared-clusters", len(sharedMatched)))

	legacy, err := p.inspectProjects(ctx, matched)
	if err != nil {
		return nil, err
	}

	return append(legacy, sharedMatched...), nil
}

// inspectProjects skips a project deleted since the ListProjects call.
func (p *Deployer) inspectProjects(ctx context.Context, projects []cbdc2Project) ([]*clusterInfo, error) {
	if len(projects) == 0 {
		return nil, nil
	}

	if len(projects) == 1 {
		info, err := p.inspectProject(ctx, projects[0])
		if err != nil {
			if capellav4.IsProjectNotFound(err) {
				return nil, nil
			}
			return nil, err
		}

		return []*clusterInfo{info}, nil
	}

	inspectCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	results := make([]*clusterInfo, len(projects))
	sem := make(chan struct{}, maxProjectInspectConcurrency)

	for i, project := range projects {
		wg.Add(1)
		go func(i int, project cbdc2Project) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			if inspectCtx.Err() != nil {
				return
			}

			info, err := p.inspectProject(inspectCtx, project)
			if err != nil {
				if capellav4.IsProjectNotFound(err) {
					results[i] = nil
					return
				}
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}

			results[i] = info
		}(i, project)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	var out []*clusterInfo
	for _, info := range results {
		if info != nil {
			out = append(out, info)
		}
	}

	return out, nil
}

var errClusterNotFound = errors.New("failed to find cluster")

// findClusterInfo returns the cluster whose meta ID matches, with no check on
// its state. In the old layout it also finds an empty or a corrupted project.
// The old layout is checked first because it needs no extra call.
func (p *Deployer) findClusterInfo(ctx context.Context, clusterID string) (*clusterInfo, error) {
	shared, projects, err := p.listProjects(ctx)
	if err != nil {
		return nil, err
	}

	for _, project := range projects {
		if project.Meta.ID.String() == clusterID {
			return p.inspectProject(ctx, project)
		}
	}

	if shared != nil {
		sharedClusters, err := p.listSharedClusters(ctx, shared)
		if err != nil {
			return nil, err
		}

		for _, cluster := range sharedClusters {
			if cluster.Meta.ID.String() == clusterID {
				return cluster, nil
			}
		}
	}

	return nil, errClusterNotFound
}

func (p *Deployer) getCluster(ctx context.Context, clusterID string) (*clusterInfo, error) {
	foundCluster, err := p.findClusterInfo(ctx, clusterID)
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

// The v4 analytics API exposes no connection string, certificate or database
// credentials, so columnar clusters still need the v2 record.
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
		PerPage:       1000,
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

func (p *Deployer) toClusterInfo(cluster *clusterInfo) *ClusterInfo {
	if cluster.IsCorrupted {
		return &ClusterInfo{
			ClusterID:      cluster.Meta.ID.String(),
			Purpose:        cluster.Meta.Purpose,
			Type:           deployment.ClusterTypeUnknown,
			CloudProjectID: cluster.ProjectID,
			CloudClusterID: "",
			CloudProvider:  "",
			Region:         "",
			Expiry:         cluster.Meta.Expiry,
			State:          "corrupted",
		}
	}

	if cluster.Cluster == nil && cluster.Columnar == nil {
		return &ClusterInfo{
			ClusterID:      cluster.Meta.ID.String(),
			Purpose:        cluster.Meta.Purpose,
			Type:           deployment.ClusterTypeUnknown,
			CloudProjectID: cluster.ProjectID,
			CloudClusterID: "",
			CloudProvider:  "",
			Region:         "",
			Expiry:         cluster.Meta.Expiry,
			State:          "provisioning",
		}
	}

	if cluster.Cluster != nil {
		return &ClusterInfo{
			ClusterID:      cluster.Meta.ID.String(),
			Purpose:        cluster.Meta.Purpose,
			Type:           deployment.ClusterTypeServer,
			CloudProjectID: cluster.ProjectID,
			CloudClusterID: cluster.Cluster.ID,
			CloudProvider:  cluster.Cluster.CloudProvider.Type,
			Region:         cluster.Cluster.CloudProvider.Region,
			Expiry:         cluster.Meta.Expiry,
			State:          cluster.Cluster.CurrentState,
		}
	}

	return &ClusterInfo{
		ClusterID:      cluster.Meta.ID.String(),
		Purpose:        cluster.Meta.Purpose,
		Type:           deployment.ClusterTypeColumnar,
		CloudProjectID: cluster.ProjectID,
		CloudClusterID: cluster.Columnar.ID,
		CloudProvider:  cluster.Columnar.CloudProviderName(),
		Region:         cluster.Columnar.Region,
		Expiry:         cluster.Meta.Expiry,
		State:          cluster.Columnar.CurrentState,
	}
}

func (p *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	return p.FindClusters(ctx, "")
}

func (p *Deployer) FindClusters(ctx context.Context, idPrefix string) ([]deployment.ClusterInfo, error) {
	clusters, err := p.findClusters(ctx, idPrefix)
	if err != nil {
		return nil, err
	}

	var out []deployment.ClusterInfo
	for _, cluster := range clusters {
		out = append(out, p.toClusterInfo(cluster))
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

func (p *Deployer) deployNewCluster(ctx context.Context, def *clusterdef.Cluster, clusterVersion string, serverImage string) (deployment.ClusterInfo, error) {
	if err := p.requireLegacy("custom server image deployment"); err != nil {
		return nil, err
	}

	cloudProjectID, err := p.requireProjectID()
	if err != nil {
		return nil, err
	}

	clusterID := cbdcuuid.New()

	expiryTime := time.Time{}
	if def.Expiry > 0 {
		expiryTime = time.Now().Add(def.Expiry)
	}

	metaData := stringclustermeta.MetaData{
		ID:      clusterID,
		Expiry:  expiryTime,
		Purpose: def.Purpose,
	}
	clusterName, purpose := p.clusterNameFor(metaData)

	cloudProvider, cloudRegion, err := p.resolveCloudLocation(def)
	if err != nil {
		return nil, err
	}

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

	p.logger.Debug("creating a new cloud cluster")

	specs, err := p.buildDeploySpecs(
		ctx,
		cloudProvider,
		def.NodeGroups)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build cluster specs")
	}

	createReq := &capellacontrol.DeployClusterRequest{
		// An empty CIDR makes Capella allocate a free block.
		CIDR:        def.Cloud.Cidr,
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

	// A cluster that never goes healthy stays in place, so the debris can be
	// inspected. Cleanup takes it once it expires.
	err = p.mgr.WaitForClusterState(ctx, p.tenantID, cloudClusterID, "healthy", false)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for cluster deployment")
	}

	// The deployment already waited for the healthy state, so a read back adds nothing.
	return &ClusterInfo{
		ClusterID:      clusterID.String(),
		Purpose:        purpose,
		Type:           deployment.ClusterTypeServer,
		CloudProjectID: cloudProjectID,
		CloudClusterID: cloudClusterID,
		CloudProvider:  cloudProvider,
		Region:         cloudRegion,
		Expiry:         metaData.Expiry,
		State:          capellav4.StateHealthy,
	}, nil
}

// requireProjectID returns the project every new cluster goes into.
// cbdinocluster never creates this project on allocate.
func (p *Deployer) requireProjectID() (string, error) {
	if p.projectID == "" {
		return "", errors.New(`a capella project id is required, run "cbdinocluster init" to find or create the CBDC2_SHARED project, or run "cbdinocluster cloud projects create <name>" and set the id with "cbdinocluster init --capella-project-id <id>"`)
	}
	return p.projectID, nil
}

func (p *Deployer) wrapCreateError(err error) error {
	if capellav4.IsProjectNotFound(err) {
		return errors.Wrapf(err, "the configured capella project %s does not exist", p.projectID)
	}
	return errors.Wrap(err, "failed to create cluster")
}

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
	cloudProjectID, err := p.requireProjectID()
	if err != nil {
		return nil, err
	}

	clusterID := cbdcuuid.New()

	expiryTime := time.Time{}
	if def.Expiry > 0 {
		expiryTime = time.Now().Add(def.Expiry)
	}

	metaData := stringclustermeta.MetaData{
		ID:      clusterID,
		Expiry:  expiryTime,
		Purpose: def.Purpose,
	}
	clusterName, purpose := p.clusterNameFor(metaData)

	cloudProvider, cloudRegion, err := p.resolveCloudLocation(def)
	if err != nil {
		return nil, err
	}

	p.logger.Debug("creating a new cloud cluster")

	// An empty CIDR makes Capella allocate a free block.
	cloudProviderSpec := capellav4.CloudProvider{
		Type:   cloudProvider,
		Region: cloudRegion,
		Cidr:   def.Cloud.Cidr,
	}

	// A cluster that never goes healthy stays in place, so the debris can be
	// inspected. Cleanup takes it once it expires.
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
			return nil, p.wrapCreateError(err)
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
			return nil, p.wrapCreateError(err)
		}

		cloudClusterID = newCluster.ID

		p.logger.Debug("waiting for creation to complete")

		err = p.v4mgr.WaitForClusterState(ctx, p.tenantID, cloudProjectID, cloudClusterID, capellav4.StateHealthy)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wait for deployment")
		}
	} else {
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

	clusterType := deployment.ClusterTypeServer
	if def.Columnar {
		clusterType = deployment.ClusterTypeColumnar
	}

	// Every branch above waited for the healthy state, so a read back adds nothing.
	return &ClusterInfo{
		ClusterID:      clusterID.String(),
		Purpose:        purpose,
		Type:           clusterType,
		CloudProjectID: cloudProjectID,
		CloudClusterID: cloudClusterID,
		CloudProvider:  cloudProvider,
		Region:         cloudRegion,
		Expiry:         metaData.Expiry,
		State:          capellav4.StateHealthy,
	}, nil
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

	metaData := *clusterInfo.Meta
	metaData.Expiry = newExpiryTime

	if !clusterInfo.Legacy {
		newName, _ := d.clusterNameFor(metaData)
		return d.renameSharedCluster(ctx, clusterInfo, newName)
	}

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

// renameSharedCluster changes only the cluster name and never the project. The
// rename sends the current spec back so the cluster does not scale.
func (d *Deployer) renameSharedCluster(ctx context.Context, clusterInfo *clusterInfo, newName string) error {
	if clusterInfo.Columnar != nil {
		if err := d.requireLegacy("renaming a columnar cluster"); err != nil {
			return err
		}

		err := d.client.UpdateColumnarSpecs(ctx, d.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID,
			&capellacontrol.UpdateColumnarInstanceRequest{
				Name:        newName,
				Description: clusterInfo.Columnar.Description,
				Nodes:       clusterInfo.Columnar.Nodes,
			})
		if err != nil {
			return errors.Wrap(err, "failed to rename the columnar cluster")
		}

		return nil
	}

	cluster := clusterInfo.Cluster
	err := d.v4.UpdateCluster(ctx, d.tenantID, clusterInfo.ProjectID, cluster.ID,
		&capellav4.UpdateClusterRequest{
			Name:          newName,
			Description:   cluster.Description,
			Support:       cluster.Support,
			ServiceGroups: cluster.ServiceGroups,
		})
	if err == nil {
		return nil
	}

	// A free tier cluster has its own update endpoint, and the generic cluster
	// record does not identify the tier, so fall back to the free tier endpoint.
	ftErr := d.v4.UpdateFreeTierCluster(ctx, d.tenantID, clusterInfo.ProjectID, cluster.ID,
		&capellav4.UpdateFreeTierClusterRequest{
			Name:        newName,
			Description: cluster.Description,
		})
	if ftErr == nil {
		return nil
	}

	return errors.Wrap(multierr.Combine(err, ftErr), "failed to rename the cluster")
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

	// Only the v2 image override can change the server version.
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

func (d *Deployer) UpgradeCluster(ctx context.Context, clusterID string, CurrentImages string, NewImage string) error {
	if err := d.requireSupportToken("cluster image upgrade"); err != nil {
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

	waitForState := func(desiredState string) error {
		if columnar {
			return d.v4mgr.WaitForAnalyticsClusterState(ctx, d.tenantID, clusterInfo.ProjectID, instanceId, desiredState)
		}
		return d.v4mgr.WaitForClusterState(ctx, d.tenantID, clusterInfo.ProjectID, instanceId, desiredState)
	}

	err = waitForState(capellav4.StateUpgrading)
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster upgrade to begin")
	}

	d.logger.Debug("waiting for cluster to be healthy")

	err = waitForState(capellav4.StateHealthy)
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

// canDeleteProjectName reports if cbdinocluster owns the project, which means
// the project name parses as cbdc2 meta data.
func canDeleteProjectName(projectName string) bool {
	meta, err := stringclustermeta.Parse(projectName)
	return err == nil && meta != nil
}

// deleteProject is the only path that may delete a project, so the ownership
// guard covers every caller.
//
// Two sweeps can race on one project, so a not found answer counts as removed.
func (p *Deployer) deleteProject(ctx context.Context, projectID string, projectName string) error {
	// The configured project holds the clusters of other users. A name that
	// parses as cbdc2 meta data does not make it an old layout project.
	if p.projectID != "" && projectID == p.projectID {
		return errors.Errorf("refusing to delete project %s, it is the configured capella project",
			projectID)
	}
	if !canDeleteProjectName(projectName) {
		return errors.Errorf("refusing to delete project %s, the name %q is not owned by cbdinocluster",
			projectID, projectName)
	}

	err := p.v4.DeleteProject(ctx, p.tenantID, projectID)
	if capellav4.IsProjectNotFound(err) {
		p.logger.Info("project already removed", zap.String("project-id", projectID))
		return nil
	}

	return err
}

// A free tier cluster has its own delete endpoint, and the generic cluster
// record does not identify the tier, so fall back to the free tier endpoint
// when the generic delete is rejected.
func (p *Deployer) deleteCloudCluster(ctx context.Context, projectID, clusterID string) error {
	err := p.v4.DeleteCluster(ctx, p.tenantID, projectID, clusterID)
	if err == nil {
		return nil
	}
	ftErr := p.v4.DeleteFreeTierCluster(ctx, p.tenantID, projectID, clusterID)
	if ftErr == nil {
		return nil
	}
	if capellav4.IsNotFound(err) && capellav4.IsNotFound(ftErr) {
		p.logger.Info("cluster already removed", zap.String("cluster-id", clusterID))
		return nil
	}
	return multierr.Combine(err, ftErr)
}

func (p *Deployer) removeCluster(ctx context.Context, clusterInfo *clusterInfo) error {
	p.logger.Debug("deleting the cloud cluster", zap.String("cluster-id", clusterInfo.Meta.ID.String()))

	if clusterInfo.IsCorrupted {
		// A corrupted project holds more than one cluster, and Capella refuses
		// to delete a project that still holds any, so remove them all first.
		targets, _, err := p.listRemovalTargets(ctx, clusterInfo.ProjectID, false)
		if err != nil {
			return errors.Wrap(err, "failed to list the clusters of the corrupted project")
		}

		_, err = p.removeTargets(ctx, targets)
		if err != nil {
			return err
		}
	} else if clusterInfo.Cluster != nil {
		err := p.deleteCloudCluster(ctx, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.v4mgr.WaitForClusterState(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID, capellav4.StateDeleted)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster destruction")
		}
	} else if clusterInfo.Columnar != nil {
		// The deletion wait needs the underlying cloud cluster ID, which only the
		// v2 record carries.
		detail, err := p.columnarV2Detail(ctx, clusterInfo)
		if err != nil {
			return err
		}

		err = p.client.DeleteColumnar(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Columnar.ID)
		if err != nil && !capellav4.IsNotFound(err) {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.mgr.WaitForColumnarDeletion(ctx, p.tenantID, clusterInfo.Columnar.ID, detail.Config.Id)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster destruction")
		}
	}

	// The shared project holds other clusters, so only the old layout drops it.
	if !clusterInfo.Legacy {
		return nil
	}

	p.logger.Debug("deleting the cloud project")

	err := p.deleteProject(ctx, clusterInfo.ProjectID, clusterInfo.ProjectName)
	if err != nil {
		return errors.Wrap(err, "failed to delete project")
	}

	return nil
}

// RemoveCluster does not use getCluster on purpose, so in the old layout it
// also removes an empty project and a corrupted one.
func (p *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	clusterInfo, err := p.findClusterInfo(ctx, clusterID)
	// A sweep can remove the cluster after the caller found it.
	if errors.Is(err, errClusterNotFound) || capellav4.IsProjectNotFound(err) {
		p.logger.Info("cluster already removed", zap.String("cluster-id", clusterID))
		return nil
	}
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

		// The v4 API reports the private DNS name with the endpoint list, not with
		// the service.
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

type removalTarget struct {
	projectID string
	clusterID string
	// The cloud cluster behind a columnar instance, needed by its deletion wait.
	underlyingID string
	isColumnar   bool
}

// skipReason decides if a removal leaves a cluster alone, and returns the log
// message that says why. An empty result means the cluster goes. Capella failed
// to destroy a destroyFailed cluster, so asking again does nothing and a human
// must act. Capella already deletes a destroying cluster, so a wait on it only
// holds the cleanup back.
func skipReason(currentState string, skipStuck bool) string {
	if !skipStuck {
		return ""
	}
	switch currentState {
	case capellav4.StateDestroyFailed:
		return "skipping cluster in destroyFailed state, it needs manual removal"
	case capellav4.StateDestroying:
		return "skipping expired cluster in destroying state"
	}
	return ""
}

// listRemovalTargets returns every cluster the project holds, with the extra
// lookup a columnar deletion wait needs. It can return targets next to an
// error when only a part of the listing failed. A gone project holds nothing,
// so it returns no targets and no error. A non empty keepReason reports a
// cluster left alone, which must stop the project delete.
func (p *Deployer) listRemovalTargets(ctx context.Context, projectID string, skipStuck bool) ([]removalTarget, string, error) {
	var errs error
	var targets []removalTarget
	keepReason := ""

	clusters, err := p.v4.ListClusters(ctx, p.tenantID, projectID)
	if capellav4.IsProjectNotFound(err) {
		p.logger.Info("project already removed", zap.String("project-id", projectID))
		return nil, "", nil
	} else if err != nil {
		errs = multierr.Append(errs, errors.Wrap(err, "failed to list clusters"))
	} else {
		for _, cluster := range clusters {
			if reason := skipReason(cluster.CurrentState, skipStuck); reason != "" {
				p.logger.Info(reason,
					zap.String("cluster-id", cluster.ID),
					zap.String("project-id", projectID))
				keepReason = reason
				continue
			}

			targets = append(targets, removalTarget{
				projectID: projectID,
				clusterID: cluster.ID,
			})
		}
	}

	columnars, err := p.v4.ListAnalyticsClusters(ctx, p.tenantID, projectID)
	if capellav4.IsProjectNotFound(err) {
		p.logger.Info("project already removed", zap.String("project-id", projectID))
		return nil, "", nil
	} else if err != nil {
		errs = multierr.Append(errs, errors.Wrap(err, "failed to list analytics clusters"))
	} else {
		for _, columnar := range columnars {
			if reason := skipReason(columnar.CurrentState, skipStuck); reason != "" {
				p.logger.Info(reason,
					zap.String("cluster-id", columnar.ID),
					zap.String("project-id", projectID))
				keepReason = reason
				continue
			}

			detail, err := p.columnarV2DetailByID(ctx, columnar.ID)
			if err != nil {
				errs = multierr.Append(errs, err)
				continue
			}

			targets = append(targets, removalTarget{
				projectID:    projectID,
				clusterID:    columnar.ID,
				underlyingID: detail.Config.Id,
				isColumnar:   true,
			})
		}
	}

	return targets, keepReason, errs
}

// removeTargets deletes the clusters first and waits after, so the deletions
// run in parallel on the Capella side. It reports the projects that still
// hold a cluster, which Capella refuses to delete.
func (p *Deployer) removeTargets(ctx context.Context, targets []removalTarget) (map[string]bool, error) {
	var errs error
	failedProjects := make(map[string]bool)
	deleteFailed := make([]bool, len(targets))

	for i, target := range targets {
		p.logger.Info("removing a cluster", zap.String("cluster-id", target.clusterID))

		var err error
		if target.isColumnar {
			err = p.client.DeleteColumnar(ctx, p.tenantID, target.projectID, target.clusterID)
			if capellav4.IsNotFound(err) {
				err = nil
			}
		} else {
			err = p.deleteCloudCluster(ctx, target.projectID, target.clusterID)
		}
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to remove cluster"))
			failedProjects[target.projectID] = true
			deleteFailed[i] = true
		}
	}

	for i, target := range targets {
		// A cluster whose delete failed never reaches the deleted state, so a
		// wait on it runs until the deadline.
		if deleteFailed[i] {
			continue
		}

		p.logger.Info("waiting for cluster removal to complete", zap.String("cluster-id", target.clusterID))

		var err error
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

	return failedProjects, errs
}

// cleanupShouldTake decides if a cleanup takes a shared cluster or an old
// layout project.
func cleanupShouldTake(meta *stringclustermeta.MetaData, opts deployment.CleanupOptions, now time.Time) bool {
	// A zero expiry means the cluster never expires.
	if meta.Expiry.IsZero() || meta.Expiry.After(now) {
		return false
	}
	return deployment.PurposeMatches(meta.Purpose, opts.Purpose)
}

func (p *Deployer) RemoveAll(ctx context.Context) error {
	return p.removeAll(ctx, deployment.RemoveAllOptions{})
}

func (p *Deployer) RemoveAllScoped(ctx context.Context, opts deployment.RemoveAllOptions) error {
	return p.removeAll(ctx, opts)
}

// selectSharedClusters picks the shared clusters a removal takes. inScope is
// the cleanup or remove-all scope rule. keep holds the clusters in scope that
// skipReason leaves alone, which a cleanup does not delete or wait on. It is
// pure so tests can cover the rules without API calls.
func selectSharedClusters(
	clusters []*clusterInfo,
	inScope func(meta *stringclustermeta.MetaData) bool,
	skipStuck bool,
) (take []*clusterInfo, keep []*clusterInfo) {
	for _, cluster := range clusters {
		if !inScope(cluster.Meta) {
			continue
		}
		if skipReason(sharedClusterState(cluster), skipStuck) != "" {
			keep = append(keep, cluster)
			continue
		}
		take = append(take, cluster)
	}
	return take, keep
}

func sharedClusterState(cluster *clusterInfo) string {
	if cluster.Columnar != nil {
		return cluster.Columnar.CurrentState
	}
	return cluster.Cluster.CurrentState
}

func sharedCloudClusterID(cluster *clusterInfo) string {
	if cluster.Columnar != nil {
		return cluster.Columnar.ID
	}
	return cluster.Cluster.ID
}

func sharedClusterName(cluster *clusterInfo) string {
	if cluster.Columnar != nil {
		return cluster.Columnar.Name
	}
	return cluster.Cluster.Name
}

// sharedRemovalTarget adds the lookup a columnar deletion wait needs.
func (p *Deployer) sharedRemovalTarget(ctx context.Context, cluster *clusterInfo) (removalTarget, error) {
	if cluster.Columnar == nil {
		return removalTarget{
			projectID: cluster.ProjectID,
			clusterID: cluster.Cluster.ID,
		}, nil
	}

	detail, err := p.columnarV2DetailByID(ctx, cluster.Columnar.ID)
	if err != nil {
		return removalTarget{}, err
	}

	return removalTarget{
		projectID:    cluster.ProjectID,
		clusterID:    cluster.Columnar.ID,
		underlyingID: detail.Config.Id,
		isColumnar:   true,
	}, nil
}

// listSharedForRemoval returns no clusters when no project ID is set.
func (p *Deployer) listSharedForRemoval(ctx context.Context, shared *capellav4.ProjectInfo) ([]*clusterInfo, error) {
	if shared == nil {
		return nil, nil
	}

	clusters, err := p.listSharedClusters(ctx, shared)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list the clusters of the shared project")
	}

	return clusters, nil
}

func (p *Deployer) logSkippedSharedClusters(clusters []*clusterInfo) {
	for _, cluster := range clusters {
		p.logger.Info(skipReason(sharedClusterState(cluster), true),
			zap.String("cluster-id", sharedCloudClusterID(cluster)),
			zap.String("project-id", cluster.ProjectID))
	}
}

// removeClusters deletes every cluster of the old layout projects and every
// taken shared cluster first, and waits after, so the deletions overlap on the
// Capella side. It then deletes the old layout projects whose clusters all
// went. An empty old layout project holds no target, so it goes straight away.
// The shared project is never deleted, because it is not in projects.
func (p *Deployer) removeClusters(
	ctx context.Context,
	projects []cbdc2Project,
	sharedClusters []*clusterInfo,
	skipStuck bool,
) error {
	var errs error

	var targets []removalTarget
	keptProjects := make(map[string]bool)
	for _, project := range projects {
		projectTargets, keepReason, err := p.listRemovalTargets(ctx, project.Info.ID, skipStuck)
		if err != nil {
			errs = multierr.Append(errs, err)
			keptProjects[project.Info.ID] = true
		}
		if keepReason != "" {
			keptProjects[project.Info.ID] = true
		}

		targets = append(targets, projectTargets...)
	}

	for _, cluster := range sharedClusters {
		target, err := p.sharedRemovalTarget(ctx, cluster)
		if err != nil {
			errs = multierr.Append(errs, err)
			continue
		}

		targets = append(targets, target)
	}

	p.logger.Info("found clusters to remove", zap.Int("count", len(targets)))

	removalFailed, err := p.removeTargets(ctx, targets)
	if err != nil {
		errs = multierr.Append(errs, err)
	}
	for projectID := range removalFailed {
		keptProjects[projectID] = true
	}

	// Capella refuses to delete a project that still holds a cluster.
	for _, project := range projects {
		if keptProjects[project.Info.ID] {
			p.logger.Warn("keeping project as its clusters were not all removed",
				zap.String("project-id", project.Info.ID))
			continue
		}

		p.logger.Info("removing a project", zap.String("project-id", project.Info.ID))

		err := p.deleteProject(ctx, project.Info.ID, project.Info.Name)
		if err != nil {
			errs = multierr.Append(errs, errors.Wrap(err, "failed to remove project"))
		}
	}

	if errs != nil {
		return multierr.Combine(errs)
	}

	return nil
}

// dryRunRemoveProjects lists the clusters of the old layout projects like
// removeClusters does, so it keeps the same projects. It cannot predict a
// delete that fails during the real run. reason only labels the output. It
// returns how many projects would be removed and kept.
func (p *Deployer) dryRunRemoveProjects(
	ctx context.Context,
	projects []cbdc2Project,
	skipStuck bool,
	reason string,
) (int, int, error) {
	var errs error
	removed := 0
	kept := 0

	for _, project := range projects {
		targets, keepReason, err := p.listRemovalTargets(ctx, project.Info.ID, skipStuck)
		if err != nil {
			errs = multierr.Append(errs, err)
			kept++
			p.logger.Warn("dry run, would keep the project, listing its clusters failed",
				zap.String("project-id", project.Info.ID),
				zap.String("project-name", project.Info.Name),
				zap.String("purpose", project.Meta.Purpose),
				zap.Error(err))
			continue
		}
		if keepReason != "" {
			kept++
			p.logger.Info("dry run, would keep the project, a cluster is skipped",
				zap.String("project-id", project.Info.ID),
				zap.String("project-name", project.Info.Name),
				zap.String("purpose", project.Meta.Purpose),
				zap.String("reason", keepReason))
			continue
		}

		removed++
		p.logger.Info("dry run, would remove the project and its clusters",
			zap.String("project-id", project.Info.ID),
			zap.String("project-name", project.Info.Name),
			zap.String("purpose", project.Meta.Purpose),
			zap.Time("expiry", project.Meta.Expiry),
			zap.String("reason", reason),
			zap.Int("clusters", len(targets)))
	}

	return removed, kept, errs
}

// dryRunRemoveSharedClusters does the same lookups as removeClusters for the
// taken shared clusters, so it reports the same results. keep holds the
// clusters a cleanup skips. It returns how many clusters would be removed and
// kept.
func (p *Deployer) dryRunRemoveSharedClusters(
	ctx context.Context,
	take []*clusterInfo,
	keep []*clusterInfo,
	reason string,
) (int, int, error) {
	var errs error
	removed := 0
	kept := len(keep)

	for _, cluster := range keep {
		p.logger.Info("dry run, would keep the cluster, it is skipped",
			zap.String("cluster-name", sharedClusterName(cluster)),
			zap.String("cloud-cluster-id", sharedCloudClusterID(cluster)),
			zap.String("purpose", cluster.Meta.Purpose),
			zap.String("reason", skipReason(sharedClusterState(cluster), true)))
	}

	for _, cluster := range take {
		_, err := p.sharedRemovalTarget(ctx, cluster)
		if err != nil {
			errs = multierr.Append(errs, err)
			kept++
			p.logger.Warn("dry run, would keep the cluster, its lookup failed",
				zap.String("cluster-name", sharedClusterName(cluster)),
				zap.String("cloud-cluster-id", sharedCloudClusterID(cluster)),
				zap.String("purpose", cluster.Meta.Purpose),
				zap.Error(err))
			continue
		}

		removed++
		p.logger.Info("dry run, would remove the cluster",
			zap.String("cluster-name", sharedClusterName(cluster)),
			zap.String("cloud-cluster-id", sharedCloudClusterID(cluster)),
			zap.String("purpose", cluster.Meta.Purpose),
			zap.Time("expiry", cluster.Meta.Expiry),
			zap.String("reason", reason))
	}

	return removed, kept, errs
}

func (p *Deployer) removeAll(ctx context.Context, opts deployment.RemoveAllOptions) error {
	shared, allProjects, err := p.listProjects(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list projects")
	}

	// A failed shared listing must not block the old layout projects.
	allShared, sharedListErr := p.listSharedForRemoval(ctx, shared)

	var projects []cbdc2Project
	for _, project := range allProjects {
		if deployment.PurposeMatches(project.Meta.Purpose, opts.Purpose) {
			projects = append(projects, project)
		}
	}

	// A remove-all keeps trying a destroyFailed cluster. The delete fails, an
	// old layout project is kept and the error is reported. It also waits on a
	// destroying cluster, up to the --timeout limit.
	sharedTake, _ := selectSharedClusters(allShared, func(meta *stringclustermeta.MetaData) bool {
		return deployment.PurposeMatches(meta.Purpose, opts.Purpose)
	}, false)

	if opts.DryRun {
		removed, kept, err := p.dryRunRemoveProjects(ctx, projects, false, "in scope")
		sharedRemoved, sharedKept, sharedErr := p.dryRunRemoveSharedClusters(ctx, sharedTake, nil, "in scope")
		p.logger.Info("dry run finished, nothing was removed",
			zap.Int("projects-in-scope", len(projects)),
			zap.Int("projects-would-remove", removed),
			zap.Int("projects-would-keep", kept),
			zap.Int("projects-total", len(allProjects)),
			zap.Int("shared-clusters-in-scope", len(sharedTake)),
			zap.Int("shared-clusters-would-remove", sharedRemoved),
			zap.Int("shared-clusters-would-keep", sharedKept),
			zap.Int("shared-clusters-total", len(allShared)))
		return multierr.Combine(sharedListErr, err, sharedErr)
	}

	return multierr.Combine(sharedListErr, p.removeClusters(ctx, projects, sharedTake, false))
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
		// The v4 API can return this connection string with or without its scheme.
		srvName := strings.TrimPrefix(clusterInfo.Cluster.ConnectionString, "couchbases://")
		connStr = fmt.Sprintf("couchbases://%s", srvName)
		dnsSRV = srvName

		// The Data API connection string is a separate resource. A cluster without
		// the Data API must still report its normal connection string.
		dataApi, err := p.v4.GetDataApi(ctx, p.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
		if err != nil {
			p.logger.Debug("failed to fetch data api details", zap.Error(err))
		} else if dataApi.ConnectionString != "" {
			// The v4 API returns this connection string with its scheme.
			dataApiConnstr = dataApi.ConnectionString
			if !strings.HasPrefix(dataApiConnstr, "https://") {
				dataApiConnstr = "https://" + dataApiConnstr
			}
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
	return p.cleanup(ctx, deployment.CleanupOptions{})
}

func (p *Deployer) CleanupScoped(ctx context.Context, opts deployment.CleanupOptions) error {
	return p.cleanup(ctx, opts)
}

// cleanup is a remove-all restricted to the expired shared clusters and old
// layout projects, plus the skip of a cluster Capella failed to destroy or
// already destroys.
func (p *Deployer) cleanup(ctx context.Context, opts deployment.CleanupOptions) error {
	shared, allProjects, err := p.listProjects(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list projects")
	}

	// A failed shared listing must not block the old layout projects.
	allShared, sharedListErr := p.listSharedForRemoval(ctx, shared)

	// In the old layout an allocate creates the project first, so an unexpired
	// empty project may belong to a run still in flight. Only the expired go.
	now := time.Now()
	var projects []cbdc2Project
	for _, project := range allProjects {
		if cleanupShouldTake(project.Meta, opts, now) {
			projects = append(projects, project)
		}
	}

	sharedTake, sharedKeep := selectSharedClusters(allShared, func(meta *stringclustermeta.MetaData) bool {
		return cleanupShouldTake(meta, opts, now)
	}, true)

	if opts.DryRun {
		removed, kept, err := p.dryRunRemoveProjects(ctx, projects, true, "expired")
		sharedRemoved, sharedKept, sharedErr := p.dryRunRemoveSharedClusters(ctx, sharedTake, sharedKeep, "expired")
		fields := []zap.Field{
			zap.Int("projects-expired", len(projects)),
			zap.Int("projects-would-remove", removed),
			zap.Int("projects-would-keep", kept),
			zap.Int("projects-total", len(allProjects)),
			zap.Int("shared-clusters-expired", len(sharedTake)+len(sharedKeep)),
			zap.Int("shared-clusters-would-remove", sharedRemoved),
			zap.Int("shared-clusters-would-keep", sharedKept),
			zap.Int("shared-clusters-total", len(allShared)),
		}
		if opts.Purpose != "" {
			fields = append(fields, zap.String("purpose", opts.Purpose))
		}
		p.logger.Info("dry run finished, nothing was removed", fields...)
		return multierr.Combine(sharedListErr, err, sharedErr)
	}

	p.logSkippedSharedClusters(sharedKeep)
	p.logCleanupScope(opts.Purpose, shared, projects, len(sharedTake))

	return multierr.Combine(sharedListErr, p.removeClusters(ctx, projects, sharedTake, true))
}

func (p *Deployer) logCleanupScope(purpose string, shared *capellav4.ProjectInfo, projects []cbdc2Project, sharedCount int) {
	var purposeFields []zap.Field
	if purpose != "" {
		purposeFields = append(purposeFields, zap.String("purpose", purpose))
	}

	if len(projects) > 0 {
		projectIDs := make([]string, 0, len(projects))
		for _, project := range projects {
			projectIDs = append(projectIDs, project.Info.ID)
		}
		p.logger.Info("found expired legacy cbdc2 projects, removing them with their clusters",
			append([]zap.Field{
				zap.Int("count", len(projects)),
				zap.Strings("project-ids", projectIDs),
			}, purposeFields...)...)
	}

	if shared != nil {
		p.logger.Info("found expired clusters in the configured project",
			append([]zap.Field{
				zap.String("project-id", shared.ID),
				zap.Int("count", sharedCount),
			}, purposeFields...)...)
	}
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
		FlushEnabled:             opts.FlushEnabled,
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

func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	if strings.TrimSpace(d.uploadServerLogsHostName) == "" {
		return nil, fmt.Errorf("cannot collect server logs: no upload-server-logs host name is configured; " +
			"set it via `cbdinocluster init` (--upload-server-logs-host-name) or Capella.UploadServerLogsHostName in your config")
	}

	if err := d.requireSupportToken("server log collection"); err != nil {
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

func (d *Deployer) RedeployCluster(ctx context.Context, clusterID string) error {
	if err := d.requireSupportToken("cluster redeploy"); err != nil {
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
		return errors.Wrap(err, "failed to redeploy cluster")
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

	return d.enableDataApi(ctx, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
}

func (d *Deployer) enableDataApi(ctx context.Context, cloudProjectID, cloudClusterID string) error {
	d.logger.Debug("enabling data API")

	info, err := d.v4.GetDataApi(ctx, d.tenantID, cloudProjectID, cloudClusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get Data API state")
	}

	// The update replaces both fields, so keep the network peering state.
	err = d.v4.UpdateDataApi(ctx, d.tenantID, cloudProjectID, cloudClusterID, &capellav4.UpdateDataApiRequest{
		EnableDataApi:        true,
		EnableNetworkPeering: info.EnabledForNetworkPeering,
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

func (d *Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string, opts *deployment.ExecuteQueryOptions) (string, error) {
	if opts == nil || opts.Username == "" || opts.Password == "" {
		return "", errors.New("cloud queries need the username and password of an existing database user")
	}

	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return "", err
	}
	if clusterInfo.Cluster == nil {
		return "", errors.New("queries are not supported for columnar clusters")
	}

	cert, err := d.v4.GetCertificate(ctx, d.tenantID, clusterInfo.ProjectID, clusterInfo.Cluster.ID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster certificate")
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(cert)) {
		return "", errors.New("failed to parse cluster certificate")
	}

	srvName := strings.TrimPrefix(clusterInfo.Cluster.ConnectionString, "couchbases://")
	baseSpec, err := gocbconnstr.Parse(fmt.Sprintf("couchbases://%s", srvName))
	if err != nil {
		return "", errors.Wrap(err, "failed to parse connstr")
	}

	resolvedSpec, err := gocbconnstr.Resolve(baseSpec)
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve connstr")
	}

	var httpAddrs []string
	for _, host := range resolvedSpec.HttpHosts {
		httpAddrs = append(httpAddrs, fmt.Sprintf("%s:%d", host.Host, host.Port))
	}

	var memdAddrs []string
	for _, host := range resolvedSpec.MemdHosts {
		memdAddrs = append(memdAddrs, fmt.Sprintf("%s:%d", host.Host, host.Port))
	}

	// SRV resolution returns only memd hosts, and the agent bootstraps over
	// HTTP, so the management addresses come from the same hosts.
	if len(httpAddrs) == 0 {
		for _, host := range resolvedSpec.MemdHosts {
			httpAddrs = append(httpAddrs, fmt.Sprintf("%s:%d", host.Host, 18091))
		}
	}

	// The connection times out when the caller's IP is not on the allow list.
	agent, err := gocbcorex.CreateAgent(ctx, gocbcorex.AgentOptions{
		Logger:    d.logger.Named("agent"),
		TLSConfig: &tls.Config{RootCAs: caPool},
		Authenticator: &gocbcorex.PasswordAuthenticator{
			Username: opts.Username,
			Password: opts.Password,
		},
		SeedConfig: gocbcorex.SeedConfig{
			HTTPAddrs: httpAddrs,
			MemdAddrs: memdAddrs,
		},
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to create gocbcorex agent")
	}
	defer agent.Close()

	return commondeploy.AgentHelper{Agent: agent}.ExecuteQuery(ctx, query)
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
