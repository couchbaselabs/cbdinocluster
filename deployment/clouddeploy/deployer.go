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

	"github.com/couchbase/gocbcorex/cbmgmtx"
	"github.com/couchbase/gocbcorex/cbqueryx"
	"github.com/couchbaselabs/cbdinocluster/utils/webhelper"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

type Deployer struct {
	logger                   *zap.Logger
	client                   *capellacontrol.Controller
	mgr                      *capellacontrol.Manager
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
	Logger                   *zap.Logger
	Client                   *capellacontrol.Controller
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
	return &Deployer{
		logger: opts.Logger,
		client: opts.Client,
		mgr: &capellacontrol.Manager{
			Logger: opts.Logger,
			Client: opts.Client,
		},
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

type clusterInfo struct {
	Meta        *stringclustermeta.MetaData
	Project     *capellacontrol.ProjectInfo
	Cluster     *capellacontrol.ClusterInfo
	Columnar    *capellacontrol.ColumnarData
	IsCorrupted bool
}

func (p *Deployer) listClusters(ctx context.Context) ([]*clusterInfo, error) {
	p.logger.Debug("listing cloud projects")

	projects, err := p.client.ListProjects(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       1000,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list projects")
	}

	p.logger.Debug("listing all cloud clusters")

	clusters, err := p.client.ListAllClusters(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       1000,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list all clusters")
	}

	getClustersForProject := func(projectID string) []*capellacontrol.ClusterInfo {
		var out []*capellacontrol.ClusterInfo
		for _, cluster := range clusters.Data {
			if cluster.Data.Project.Id == projectID {
				out = append(out, cluster.Data)
			}
		}
		return out
	}

	var out []*clusterInfo

	for _, project := range projects.Data {
		meta, err := stringclustermeta.Parse(project.Data.Name)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse meta-data from project name")
		}
		if meta == nil {
			// not a cbdc2 project
			continue
		}

		clusters := getClustersForProject(project.Data.ID)

		if len(clusters) == 0 {
			continue
		} else if len(clusters) > 1 {
			out = append(out, &clusterInfo{
				Meta:        meta,
				Project:     project.Data,
				Cluster:     nil,
				IsCorrupted: true,
			})
			continue
		}

		cluster := clusters[0]

		out = append(out, &clusterInfo{
			Meta:        meta,
			Project:     project.Data,
			Cluster:     cluster,
			IsCorrupted: false,
		})
	}

	columnars, err := p.client.ListAllColumnars(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       1000,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list all clusters")
	}

	getColumnarsForProject := func(projectID string) []*capellacontrol.ColumnarData {
		var out []*capellacontrol.ColumnarData
		for _, cluster := range columnars.Data {
			if cluster.Data.ProjectID == projectID {
				out = append(out, cluster.Data)
			}
		}
		return out
	}

	for _, project := range projects.Data {
		meta, err := stringclustermeta.Parse(project.Data.Name)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse meta-data from project name")
		}
		if meta == nil {
			// not a cbdc2 project
			continue
		}

		columnars := getColumnarsForProject(project.Data.ID)

		if len(columnars) == 0 {
			// Operational cluster
			continue
		} else if len(columnars) > 1 {
			out = append(out, &clusterInfo{
				Meta:        meta,
				Project:     project.Data,
				Cluster:     nil,
				IsCorrupted: true,
			})
			continue
		}

		columnar := columnars[0]

		out = append(out, &clusterInfo{
			Meta:        meta,
			Project:     project.Data,
			Columnar:    columnar,
			IsCorrupted: false,
		})
	}

	return out, nil
}

func (p *Deployer) getCluster(ctx context.Context, clusterID string) (*clusterInfo, error) {
	clusters, err := p.listClusters(ctx)
	if err != nil {
		return nil, err
	}

	var foundCluster *clusterInfo
	for _, cluster := range clusters {
		if cluster.Meta.ID.String() == clusterID {
			foundCluster = cluster
		}
	}
	if foundCluster == nil {
		return nil, errors.New("failed to find cluster")
	}

	if foundCluster.IsCorrupted {
		return nil, errors.New("found cluster, but it is in a corrupted state")
	}

	return foundCluster, nil
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
				CloudProjectID: cluster.Project.ID,
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
				CloudProjectID: cluster.Project.ID,
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
				CloudProjectID: cluster.Project.ID,
				CloudClusterID: cluster.Cluster.Id,
				CloudProvider:  cluster.Cluster.Provider.Name,
				Region:         cluster.Cluster.Provider.Region,
				Expiry:         cluster.Meta.Expiry,
				State:          cluster.Cluster.Status.State,
			})
		} else if cluster.Columnar != nil {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID.String(),
				Type:           deployment.ClusterTypeColumnar,
				CloudProjectID: cluster.Project.ID,
				CloudClusterID: cluster.Columnar.ID,
				CloudProvider:  cluster.Columnar.Config.Provider,
				Region:         cluster.Columnar.Config.Region,
				Expiry:         cluster.Meta.Expiry,
				State:          cluster.Columnar.State,
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
			cpu = 8
			memory = 32
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

		nsServices, err := clusterdef.ServicesToNsServicesOverride(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

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

func (p *Deployer) buildCreateSpecs(
	ctx context.Context,
	cloudProvider string,
	nodeGrps []*clusterdef.NodeGroup,
) ([]capellacontrol.CreateClusterRequest_Spec, error) {
	nodeProvider := ""
	diskAutoExpansionEnabled := false
	if cloudProvider == "aws" {
		nodeProvider = "aws"
		diskAutoExpansionEnabled = true
	} else if cloudProvider == "gcp" {
		nodeProvider = "gcp"
		diskAutoExpansionEnabled = true
	} else if cloudProvider == "azure" {
		nodeProvider = "azure"
		diskAutoExpansionEnabled = false
	} else {
		return nil, errors.New("invalid cloud provider for setup info")
	}

	var specs []capellacontrol.CreateClusterRequest_Spec
	for _, nodeGroup := range nodeGrps {
		var instanceType string
		var diskType string
		var diskSize int
		var diskIops int

		if cloudProvider == "aws" {
			instanceType = "m5.xlarge"
			diskType = "gp3"
			diskSize = 50
			diskIops = 3000
		} else if cloudProvider == "gcp" {
			instanceType = "n2-standard-4"
			diskType = "pd-ssd"
			diskSize = 50
		} else if cloudProvider == "azure" {
			instanceType = "Standard_D4s_v5"
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

		services := []clusterdef.Service{
			clusterdef.KvService,
			clusterdef.IndexService,
			clusterdef.QueryService,
			clusterdef.SearchService,
		}
		if len(nodeGroup.Services) > 0 {
			services = nodeGroup.Services
		}

		nsServices, err := clusterdef.ServicesToNsServices(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

		specs = append(specs, capellacontrol.CreateClusterRequest_Spec{
			Compute: instanceType,
			Count:   nodeGroup.Count,
			Disk: capellacontrol.CreateClusterRequest_Spec_Disk{
				Type:     diskType,
				SizeInGb: diskSize,
				Iops:     diskIops,
			},
			DiskAutoScaling: capellacontrol.CreateClusterRequest_Spec_DiskScaling{
				Enabled: diskAutoExpansionEnabled,
			},
			Provider: nodeProvider,
			Services: nsServices,
		})
	}

	return specs, nil
}

func (p *Deployer) buildModifySpecs(
	ctx context.Context,
	cloudProvider string,
	nodeGrps []*clusterdef.NodeGroup,
) ([]capellacontrol.UpdateClusterSpecsRequest_Spec, error) {
	createSpecs, err := p.buildCreateSpecs(ctx, cloudProvider, nodeGrps)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build the create specs")
	}

	var specs []capellacontrol.UpdateClusterSpecsRequest_Spec

	for _, spec := range createSpecs {
		specs = append(specs, capellacontrol.UpdateClusterSpecsRequest_Spec{
			Compute: capellacontrol.UpdateClusterSpecsRequest_Spec_Compute{
				Type: spec.Compute,
			},
			Count: spec.Count,
			Disk: capellacontrol.UpdateClusterSpecsRequest_Spec_Disk{
				Type:     spec.Disk.Type,
				SizeInGb: spec.Disk.SizeInGb,
				Iops:     spec.Disk.Iops,
			},
			DiskAutoScaling: capellacontrol.UpdateClusterSpecsRequest_Spec_DiskScaling{
				Enabled: spec.DiskAutoScaling.Enabled,
			},
			Services: lo.Map(spec.Services, func(spec string, _ int) capellacontrol.UpdateClusterSpecsRequest_Spec_Service {
				return capellacontrol.UpdateClusterSpecsRequest_Spec_Service{
					Type: spec,
				}
			}),
		})
	}

	return specs, nil
}

func (p *Deployer) deployNewCluster(ctx context.Context, def *clusterdef.Cluster, clusterVersion string, serverImage string) (deployment.ClusterInfo, error) {
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

	newProject, err := p.client.CreateProject(ctx, p.tenantID, &capellacontrol.CreateProjectRequest{
		Name: projectName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create project")
	}

	cloudProjectID := newProject.Id

	cloudProvider := ""
	cloudRegion := ""
	clusterCidr := ""

	if def.Cloud.CloudProvider != "" {
		cloudProvider = def.Cloud.CloudProvider
	}
	if def.Cloud.Region != "" {
		cloudRegion = def.Cloud.Region
	}
	if def.Cloud.Cidr != "" {
		clusterCidr = def.Cloud.Cidr
	}

	if cloudProvider == "" {
		cloudProvider = p.defaultCloud
	}
	if cloudRegion == "" {
		if cloudProvider == "aws" {
			cloudRegion = p.defaultAwsRegion
		} else if cloudProvider == "azure" {
			cloudRegion = p.defaultAzureRegion
		} else if cloudProvider == "gcp" {
			cloudRegion = p.defaultGcpRegion
		} else {
			return nil, errors.New("invalid cloud provider for region selection")
		}
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

	p.logger.Debug("creating a new cloud project")

	newProject, err := p.client.CreateProject(ctx, p.tenantID, &capellacontrol.CreateProjectRequest{
		Name: projectName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create project")
	}

	cloudProjectID := newProject.Id

	cloudProvider := ""
	cloudRegion := ""
	clusterCidr := ""

	if def.Cloud.CloudProvider != "" {
		cloudProvider = def.Cloud.CloudProvider
	}
	if def.Cloud.Region != "" {
		cloudRegion = def.Cloud.Region
	}
	if def.Cloud.Cidr != "" {
		clusterCidr = def.Cloud.Cidr
	}

	if cloudProvider == "" {
		cloudProvider = p.defaultCloud
	}
	if cloudRegion == "" {
		if cloudProvider == "aws" {
			cloudRegion = p.defaultAwsRegion
		} else if cloudProvider == "azure" {
			cloudRegion = p.defaultAzureRegion
		} else if cloudProvider == "gcp" {
			cloudRegion = p.defaultGcpRegion
		} else {
			return nil, errors.New("invalid cloud provider for region selection")
		}
	}

	deploymentProvider := ""
	clusterProvider := ""
	if cloudProvider == "aws" {
		deploymentProvider = "aws"
		clusterProvider = "aws"
	} else if cloudProvider == "gcp" {
		deploymentProvider = "gcp"
		if def.Columnar {
			clusterProvider = "gcp"
		} else {
			clusterProvider = "hostedGCP"
		}
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

	cloudClusterID := ""
	if def.Cloud.FreeTier {
		if len(def.NodeGroups) != 0 {
			return nil, errors.New("free-tier cluster cannot have node groups")
		}

		createReq := &capellacontrol.CreateTrialClusterRequest{
			CIDR:           clusterCidr,
			Description:    "",
			Name:           clusterName,
			ProjectId:      cloudProjectID,
			Provider:       clusterProvider,
			Region:         cloudRegion,
			Server:         clusterVersion,
			DeliveryMethod: "hosted",
		}
		p.logger.Debug("creating cluster", zap.Any("req", createReq))

		newCluster, err := p.client.CreateTrialCluster(ctx, p.tenantID, createReq)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create cluster")
		}

		cloudClusterID = newCluster.Id

		p.logger.Debug("waiting for creation to complete")

		err = p.mgr.WaitForClusterState(ctx, p.tenantID, cloudClusterID, "healthy", false)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wait for deployment")
		}
	} else if !def.Columnar {
		specs, err := p.buildCreateSpecs(
			ctx,
			cloudProvider,
			def.NodeGroups)
		if err != nil {
			return nil, errors.Wrap(err, "failed to build cluster specs")
		}

		createReq := &capellacontrol.CreateClusterRequest{
			CIDR:        clusterCidr,
			Description: "",
			Name:        clusterName,
			Plan:        "Developer Pro",
			ProjectId:   cloudProjectID,
			Provider:    clusterProvider,
			Region:      cloudRegion,
			Server:      clusterVersion,
			SingleAZ:    false,
			Specs:       specs,
			Timezone:    "PT",
		}
		p.logger.Debug("creating cluster", zap.Any("req", createReq))

		newCluster, err := p.client.CreateCluster(ctx, p.tenantID, createReq)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create cluster")
		}

		cloudClusterID = newCluster.Id

		p.logger.Debug("waiting for creation to complete")

		err = p.mgr.WaitForClusterState(ctx, p.tenantID, cloudClusterID, "healthy", false)
		if err != nil {
			return nil, errors.Wrap(err, "failed to wait for deployment")
		}
	} else {
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
			Provider:    clusterProvider,
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
			createReq.Override = &capellacontrol.CreateOverrideRequest{
				Image: def.NodeGroups[0].Cloud.ServerImage,
				Token: p.overrideToken,
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
	)
	// Ensure all node groups have the same version and image
	for _, nodeGroup := range def.NodeGroups {
		if clusterVersion == "" {
			clusterVersion = nodeGroup.Version
			serverImage = nodeGroup.Cloud.ServerImage
		} else {
			if clusterVersion != nodeGroup.Version || serverImage != nodeGroup.Cloud.ServerImage {
				return nil, errors.New("all node groups must have the same version and image")
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

	_, err = d.client.UpdateProject(
		ctx,
		d.tenantID,
		clusterInfo.Cluster.Project.Id,
		&capellacontrol.UpdateProjectRequest{
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

		newSpec := &capellacontrol.UpdateColumnarInstanceRequest{
			Name:        clusterInfo.Columnar.Name,
			Description: clusterInfo.Columnar.Description,
			Nodes:       def.NodeGroups[0].Count,
		}
		err = d.client.UpdateColumnarSpecs(ctx, clusterInfo.Columnar.TenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, newSpec)
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

	cloudProjectID := clusterInfo.Cluster.Project.Id
	cloudClusterID := clusterInfo.Cluster.Id
	cloudProvider := clusterInfo.Cluster.Provider.Name

	newSpecs, err := d.buildModifySpecs(
		ctx,
		cloudProvider,
		def.NodeGroups)
	if err != nil {
		return errors.Wrap(err, "failed to build cluster specs")
	}

	if !isServiceEqual(clusterInfo.Cluster.Services, newSpecs) {
		d.logger.Info("cluster current spec is different from the def spec")
		d.logger.Debug("generated new specification list", zap.Any("specs", newSpecs))
		err = d.client.UpdateClusterSpecs(
			ctx,
			d.tenantID,
			cloudProjectID,
			cloudClusterID,
			&capellacontrol.UpdateClusterSpecsRequest{
				Specs: newSpecs,
			})
		if err != nil {
			return errors.Wrap(err, "failed to update cluster specs")
		}

		d.logger.Debug("waiting for cluster modification to begin")

		err = d.mgr.WaitForClusterState(ctx, d.tenantID, cloudClusterID, "scaling", false)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster modification to begin")
		}

		d.logger.Debug("waiting for cluster to be healthy")

		err = d.mgr.WaitForClusterState(ctx, d.tenantID, cloudClusterID, "healthy", false)
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

	if clusterVersion != clusterInfo.Cluster.Config.Version && serverImage != "" {
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
		//time.Sleep(30 * time.Second)
		err = d.mgr.WaitForClusterState(ctx, d.tenantID, cloudClusterID, "upgrading", false)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster upgrade to begin")
		}

		err = d.mgr.WaitForClusterState(ctx, d.tenantID, cloudClusterID, "healthy", false)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster returns to healthy")
		}
	}

	return nil
}

func (d *Deployer) UpgradeCluster(ctx context.Context, clusterID string, CurrentImages string, NewImage string) error {
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
		instanceId = clusterInfo.Columnar.ID
		clusterId = clusterInfo.Columnar.Config.Id
		cloudProvider = clusterInfo.Columnar.Config.Provider
		columnar = true
	} else if clusterInfo.Cluster != nil {
		instanceId = clusterInfo.Cluster.Id
		clusterId = clusterInfo.Cluster.Id
		cloudProvider = clusterInfo.Cluster.Provider.Name
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
		err := p.client.DeleteCluster(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
		if err != nil {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.mgr.WaitForClusterState(ctx, p.tenantID, clusterInfo.Cluster.Id, "", false)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster destruction")
		}
	} else if clusterInfo.Columnar != nil {
		err := p.client.DeleteColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
		if err != nil {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.mgr.WaitForClusterState(ctx, p.tenantID, clusterInfo.Columnar.ID, "", true)
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster destruction")
		}
	}

	p.logger.Debug("deleting the cloud project")

	err := p.client.DeleteProject(ctx, p.tenantID, clusterInfo.Project.ID)
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

	var entries *capellacontrol.ListAllowListEntriesResponse
	if clusterInfo.Cluster != nil {
		entries, err = p.client.ListAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       1000,
			SortBy:        "name",
			SortDirection: "asc",
		})
	} else {
		entries, err = p.client.ListAllowListEntriesColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       1000,
			SortBy:        "name",
			SortDirection: "asc",
		})
	}

	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch allow list entries")
	}

	var out []*AllowListEntry
	for _, entry := range entries.Data {
		out = append(out, &AllowListEntry{
			ID:      entry.Data.ID,
			Cidr:    entry.Data.Cidr,
			Comment: entry.Data.Comment,
		})
	}

	return out, nil
}

func (p *Deployer) AddAllowListEntry(ctx context.Context, clusterID string, cidr string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	if clusterInfo.Cluster != nil {
		err = p.client.UpdateAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.UpdateAllowListEntriesRequest{
			Create: []capellacontrol.UpdateAllowListEntriesRequest_Entry{
				{
					Cidr:    cidr,
					Comment: "",
				},
			},
		})
	} else {
		err = p.client.AddAllowListEntryColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.UpdateAllowListEntriesRequest_Entry{
			Cidr:    cidr,
			Comment: "",
		})
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

	var entries *capellacontrol.ListAllowListEntriesResponse
	if clusterInfo.Cluster != nil {
		entries, err = p.client.ListAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       1000,
			SortBy:        "name",
			SortDirection: "asc",
		})
	} else {
		entries, err = p.client.ListAllowListEntriesColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
			Page:          1,
			PerPage:       1000,
			SortBy:        "name",
			SortDirection: "asc",
		})
	}
	if err != nil {
		return errors.Wrap(err, "failed to list allow list entries")
	}

	foundEntryId := ""
	for _, entry := range entries.Data {
		if entry.Data.Cidr == cidr {
			foundEntryId = entry.Data.ID
		}
	}

	if foundEntryId == "" {
		return errors.New("could not find matching cidr")
	}

	if clusterInfo.Cluster != nil {
		err = p.client.UpdateAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.UpdateAllowListEntriesRequest{
			Delete: []string{foundEntryId},
		})
	} else {
		err = p.client.DeleteAllowListEntryColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, foundEntryId)
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
		err = p.client.EnablePrivateEndpoints(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
		if err != nil {
			return errors.Wrap(err, "failed to enable private endpoints")
		}
		err = p.mgr.WaitForPrivateEndpointsEnabled(ctx, false, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	} else {
		err = p.client.EnablePrivateEndpointsColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
		if err != nil {
			return errors.Wrap(err, "failed to enable private endpoints")
		}
		err = p.mgr.WaitForPrivateEndpointsEnabled(ctx, true, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
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
		return p.client.DisablePrivateEndpoints(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	}
	return p.client.DisablePrivateEndpointsColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
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
		details, err := p.client.GetPrivateEndpointDetails(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch private endpoint link details")
		}

		if !details.Data.Enabled {
			return nil, errors.New("private endpoints are not enabled")
		}

		return &PrivateEndpointDetails{
			ServiceName: details.Data.ServiceName,
			PrivateDNS:  details.Data.PrivateDNS,
		}, nil
	} else {
		details, err := p.client.GetPrivateEndpointDetailsColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch private endpoint link details")
		}

		if !details.Data.Enabled {
			return nil, errors.New("private endpoints are not enabled")
		}

		return &PrivateEndpointDetails{
			ServiceName: details.Data.ServiceName,
			PrivateDNS:  details.Data.PrivateDNS,
		}, nil
	}

}

func (p *Deployer) GenPrivateEndpointLinkCommand(ctx context.Context, clusterID string, req *capellacontrol.PrivateEndpointLinkRequest) (string, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return "", err
	}

	if clusterInfo.Columnar == nil {
		cmd, err := p.client.GenPrivateEndpointLinkCommand(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PrivateEndpointLinkRequest{
			VpcID:     req.VpcID,
			SubnetIds: req.SubnetIds,
		})
		if err != nil {
			return "", errors.Wrap(err, "failed to generate private endpoint link command")
		}
		return cmd.Data.Command, nil
	} else {
		return "", errors.New("private endpoint link command generation is not supported for columnar yet")
	}
}

func (p *Deployer) AcceptPrivateEndpointLink(ctx context.Context, clusterID string, endpointID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	// in some deployment scenarios, the endpoint-id that the user has is only the
	// first part of the id, and the rest of the id comes from somewhere else, so we
	// list all of the ids, and pick the one that matches.
	var peLinks *capellacontrol.ListPrivateEndpointLinksResponse
	if clusterInfo.Columnar == nil {
		peLinks, err = p.mgr.Client.ListPrivateEndpointLinks(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	} else {
		peLinks, err = p.mgr.Client.ListPrivateEndpointLinksColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
	}
	if err != nil {
		return errors.Wrap(err, "failed to list private endpoint links")
	}

	fullEndpointId := ""

	if clusterInfo.Cluster.Provider.Name == "gcp" {
		// GCP's private endpoint implementation differs from other providers:
		// The endpoint ID is only generated after accepting the link, unlike
		// AWS/Azure where it's available before acceptance. Therefore, we use
		// the provided endpoint ID directly for GCP.
		fullEndpointId = endpointID
	}

	for _, peLink := range peLinks.Data {
		if strings.Contains(peLink.EndpointID, endpointID) {
			fullEndpointId = peLink.EndpointID
			break
		}
	}

	if fullEndpointId == "" {
		return fmt.Errorf("failed to identify endpoint '%s'", endpointID)
	}

	if clusterInfo.Columnar == nil {
		// GCP's private endpoint implementation differs from other providers:
		// The endpoint ID is only generated after accepting the link, unlike
		// AWS/Azure where it's available before acceptance. Therefore, we use
		// the provided endpoint ID directly for GCP.
		if clusterInfo.Cluster.Provider.Name != "gcp" {
			_, err = p.mgr.WaitForPrivateEndpointLink(ctx, clusterInfo.Columnar != nil, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, fullEndpointId)
			if err != nil {
				return errors.Wrap(err, "failed to wait for private endpoint link")
			}
		}

		err = p.client.AcceptPrivateEndpointLink(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PrivateEndpointAcceptLinkRequest{
			EndpointID: fullEndpointId,
		})
		if err != nil {
			return errors.Wrap(err, "failed to accept private endpoint link")
		}

		err = p.mgr.WaitForPrivateEndpointLinkState(ctx, clusterInfo.Columnar != nil, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, fullEndpointId, "linked")
		if err != nil {
			return errors.Wrap(err, "failed to wait for private endpoint link to establish")
		}

	} else {
		_, err = p.mgr.WaitForPrivateEndpointLink(ctx, clusterInfo.Columnar != nil, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, fullEndpointId)
		if err != nil {
			return errors.Wrap(err, "failed to wait for private endpoint link")
		}

		err = p.client.AcceptPrivateEndpointLinkColumnar(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PrivateEndpointAcceptLinkRequest{
			EndpointID: fullEndpointId,
		})
		if err != nil {
			return errors.Wrap(err, "failed to accept private endpoint link")
		}

		err = p.mgr.WaitForPrivateEndpointLinkState(ctx, clusterInfo.Columnar != nil, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, fullEndpointId, "linked")
		if err != nil {
			return errors.Wrap(err, "failed to wait for private endpoint link to establish")
		}
	}

	return nil
}

func (p *Deployer) RemoveAll(ctx context.Context) error {
	var errs error
	clusters, err := p.client.ListAllClusters(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		errs = multierr.Append(errs, errors.Wrap(err, "failed to list all clusters"))
	} else {
		var clustersToRemove []*capellacontrol.ClusterInfo
		for _, cluster := range clusters.Data {
			if !strings.HasPrefix(cluster.Data.Name, "cbdc2_") {
				continue
			}

			clustersToRemove = append(clustersToRemove, cluster.Data)
		}

		var clusterNamesToRemove []string
		for _, cluster := range clustersToRemove {
			clusterNamesToRemove = append(clusterNamesToRemove, cluster.Name)
		}
		p.logger.Info("found clusters to remove", zap.Strings("clusters", clusterNamesToRemove))

		for _, cluster := range clustersToRemove {
			p.logger.Info("removing a cluster", zap.String("cluster-id", cluster.Id))

			err := p.client.DeleteCluster(ctx, p.tenantID, cluster.Project.Id, cluster.Id)
			if err != nil {
				errs = multierr.Append(errs, errors.Wrap(err, "failed to remove cluster"))
			}
		}

		for _, cluster := range clustersToRemove {
			p.logger.Info("waiting for cluster removal to complete", zap.String("cluster-id", cluster.Id))

			err := p.mgr.WaitForClusterState(ctx, p.tenantID, cluster.Id, "", false)
			if err != nil {
				errs = multierr.Append(errs, errors.Wrap(err, "failed to wait for cluster to complete"))
			}
		}
	}

	columnars, err := p.client.ListAllColumnars(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       1000,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		errs = multierr.Append(errs, errors.Wrap(err, "failed to list all columnars"))
	} else {
		var columnarsToRemove []*capellacontrol.ColumnarData
		for _, columnar := range columnars.Data {
			if !strings.HasPrefix(columnar.Data.Name, "cbdc2_") {
				continue
			}

			columnarsToRemove = append(columnarsToRemove, columnar.Data)
		}

		var columnarNamesToRemove []string
		for _, cluster := range columnarsToRemove {
			columnarNamesToRemove = append(columnarNamesToRemove, cluster.Name)
		}
		p.logger.Info("found columnar to remove", zap.Strings("columnar", columnarNamesToRemove))

		for _, columnar := range columnarsToRemove {
			p.logger.Info("removing a columnar", zap.String("cluster-id", columnar.ID))

			err := p.client.DeleteColumnar(ctx, p.tenantID, columnar.ProjectID, columnar.ID)
			if err != nil {
				errs = multierr.Append(errs, errors.Wrap(err, "failed to remove columnar"))
			}
		}

		for _, columnar := range columnarsToRemove {
			p.logger.Info("waiting for cluster columnar to complete", zap.String("cluster-id", columnar.ID))

			err := p.mgr.WaitForClusterState(ctx, p.tenantID, columnar.ID, "", true)
			if err != nil {
				errs = multierr.Append(errs, errors.Wrap(err, "failed to wait for cluster to complete"))
			}
		}
	}

	projects, err := p.client.ListProjects(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		errs = multierr.Append(errs, errors.Wrap(err, "failed to list all projects"))
	} else {
		var projectsToRemove []*capellacontrol.ProjectInfo
		for _, project := range projects.Data {
			if !strings.HasPrefix(project.Data.Name, "cbdc2_") && project.Data.ClusterCount == 0 {
				continue
			}

			projectsToRemove = append(projectsToRemove, project.Data)
		}

		var projectNamesToRemove []string
		for _, project := range projectsToRemove {
			projectNamesToRemove = append(projectNamesToRemove, project.Name)
		}
		p.logger.Info("found projects to remove", zap.Strings("projects", projectNamesToRemove))

		for _, project := range projectsToRemove {
			p.logger.Info("removing a project", zap.String("project-id", project.ID))

			err := p.client.DeleteProject(ctx, p.tenantID, project.ID)
			if err != nil {
				errs = multierr.Append(errs, errors.Wrap(err, "failed to remove project"))
			}
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
		connStr = fmt.Sprintf("couchbases://%s", clusterInfo.Cluster.Connect.Srv)
		dataApiConnstr = fmt.Sprintf("https://%s", clusterInfo.Cluster.DataApiHostname)
		dnsSRV = clusterInfo.Cluster.Connect.Srv
	} else {
		connStr = fmt.Sprintf("couchbases://%s", clusterInfo.Columnar.Config.Endpoint)
		dnsSRV = clusterInfo.Columnar.Config.Endpoint
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
		if !cluster.Meta.Expiry.IsZero() && !cluster.Meta.Expiry.After(curTime) {
			p.logger.Info("removing cluster",
				zap.String("cluster-id", cluster.Meta.ID.String()))

			if cluster.Cluster != nil && cluster.Cluster.Status.State == "destroy_failed" {
				p.logger.Warn("skipping due to destroy_failed state (cluster)")
				continue
			}
			if cluster.Columnar != nil && cluster.Columnar.State == "destroy_failed" {
				p.logger.Warn("skipping due to destroy_failed state (columnar)")
				continue
			}

			err := p.removeCluster(ctx, cluster)
			if err != nil {
				allErr = multierr.Append(allErr, errors.Wrapf(err, "cluster_id: %s", cluster.Cluster.Id))
			}
		}
	}

	projects, err := p.client.ListProjects(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})

	if err != nil {
		allErr = multierr.Append(allErr, errors.Wrap(err, "failed to list all projects"))
	}

	for _, project := range projects.Data {
		if strings.HasPrefix(project.Data.Name, "cbdc2_") && project.Data.ClusterCount == 0 {
			p.logger.Info("removing project",
				zap.String("project-id", project.Data.ID))

			err := p.client.DeleteProject(ctx, p.tenantID, project.Data.ID)
			if err != nil {
				allErr = multierr.Append(allErr, errors.Wrapf(err, "project_id: %s", project.Data.ID))
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
		resp, err := p.mgr.Client.ListUsers(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PaginatedRequest{
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
			canRead := false
			canWrite := false
			for permName := range user.Data.Permissions {
				if permName == "data_writer" {
					canWrite = true
				} else if permName == "data_reader" {
					canRead = true
				}
			}

			users = append(users, deployment.UserInfo{
				Username: user.Data.Name,
				CanRead:  canRead,
				CanWrite: canWrite,
			})
		}

		return users, nil
	} else {
		resp, err := p.mgr.Client.ListColumnarUsers(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
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
		perms := make(map[string]capellacontrol.CreateUserRequest_Permission)

		if opts.CanRead {
			perms["data_reader"] = capellacontrol.CreateUserRequest_Permission{}
		}
		if opts.CanWrite {
			perms["data_writer"] = capellacontrol.CreateUserRequest_Permission{}
		}

		err = p.mgr.Client.CreateUser(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.CreateUserRequest{
			Name:           opts.Username,
			Password:       opts.Password,
			Permissions:    perms,
			CredentialType: "basic",
		})
		if err != nil {
			return errors.Wrap(err, "failed to create user")
		}
	} else {
		roles, err := p.mgr.Client.GetColumnarRoles(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
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

		err = p.mgr.Client.CreateColumnarUser(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.CreateColumnarUserRequest{
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
		resp, err := p.mgr.Client.ListUsers(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PaginatedRequest{
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

		err = p.mgr.Client.DeleteUser(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, userId)
		if err != nil {
			return errors.Wrap(err, "failed to delete user")
		}

		return nil
	} else {
		resp, err := p.mgr.Client.ListColumnarUsers(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, &capellacontrol.PaginatedRequest{
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

		err = p.mgr.Client.DeleteColumnarUser(ctx, p.tenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, userId)
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

	resp, err := p.mgr.Client.ListBuckets(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list buckets")
	}

	var buckets []deployment.BucketInfo
	for _, bucket := range resp.Buckets.Data {
		buckets = append(buckets, deployment.BucketInfo{
			Name: bucket.Data.Name,
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

	err = p.mgr.Client.CreateBucket(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.CreateBucketRequest{
		BucketConflictResolution: "seqno",
		DurabilityLevel:          "none",
		Flush:                    opts.FlushEnabled,
		MemoryAllocationInMB:     ramQuotaMb,
		Name:                     opts.Name,
		Replicas:                 numReplicas,
		StorageBackend:           "couchstore",
		Type:                     "couchbase",
	})
	if err != nil {
		return errors.Wrap(err, "failed to create bucket")
	}

	return nil
}

func (p *Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	// we can infer the bucket id by name right now
	bucketId := base64.StdEncoding.EncodeToString([]byte(bucketName))

	err = p.mgr.Client.DeleteBucket(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, bucketId)
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
		req := &capellacontrol.LoadSampleBucketRequest{Name: bucketName}
		return d.mgr.Client.LoadClusterSampleBucket(ctx, clusterInfo.Cluster.TenantId, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, req)
	}
	req := &capellacontrol.LoadColumnarSampleBucketRequest{SampleName: bucketName}
	return d.mgr.Client.LoadColumnarSampleBucket(ctx, clusterInfo.Columnar.TenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID, req)
}

func (p *Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return "", err
	}

	var resp *capellacontrol.GetTrustedCAsResponse
	if clusterInfo.Cluster != nil {
		resp, err = p.mgr.Client.GetTrustedCAs(ctx, clusterInfo.Cluster.Id)
	} else {
		resp, err = p.mgr.Client.GetTrustedCAsColumnar(ctx, clusterInfo.Columnar.TenantID, clusterInfo.Columnar.ProjectID, clusterInfo.Columnar.ID)
	}
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

func (d *Deployer) startLogCollection(ctx context.Context, cluster *clusterInfo) error {
	var startCollectingServerLogsRequest = &capellacontrol.StartCollectingServerLogsRequest{
		HostName: d.uploadServerLogsHostName,
	}

	var err = d.mgr.Client.StartCollectingServerLogs(ctx, cluster.Cluster.Id, d.internalSupportToken,
		startCollectingServerLogsRequest)

	if err != nil {
		errors.Wrap(err,
			fmt.Sprintf("failed to start server log collection: %s", err))
	} else {
		d.logger.Info(fmt.Sprintf("Log collection have started for cluster: %s", cluster.Cluster.Id))
	}

	return err
}

func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	cluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return []string{}, err
	}

	if cluster.Columnar != nil {
		return nil, errors.New("collectlogs not supported for columanr clusters yet")
	}

	err = d.startLogCollection(ctx, cluster)

	if err != nil {
		return nil, err
	}

	var downloadServerLogsRequest = &capellacontrol.DownloadServerLogsRequest{
		HostName: d.uploadServerLogsHostName,
	}

	perNodeMap, err := d.mgr.WaitForServerLogsCollected(ctx, cluster.Cluster.Id, d.internalSupportToken,
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
	cluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	if cluster.Columnar != nil {
		return errors.New("redeploy not supported for columanr clusters yet")
	}

	err = d.mgr.Client.RedeployCluster(ctx, cluster.Cluster.Id, d.internalSupportToken)

	if err != nil {
		errors.Wrap(err, "Failed to redeploy cluster")
	}

	d.logger.Debug("waiting for redeploy cluster to begin")

	err = d.mgr.WaitForClusterState(ctx, d.tenantID, cluster.Cluster.Id, "rebalancing", false)
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster modification to begin")
	}

	d.logger.Debug("waiting for cluster to be healthy")

	err = d.mgr.WaitForClusterState(ctx, d.tenantID, cluster.Cluster.Id, "healthy", false)
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

	resolvedClusterId := directID
	if directID == "" {
		clusterInfo, err := d.getCluster(ctx, clusterId)
		if err != nil {
			return err
		}
		if clusterInfo.Columnar != nil {
			return errors.Wrap(err, "can not link to another columnar cluster")
		}
		resolvedClusterId = clusterInfo.Cluster.Id
	}

	req := &capellacontrol.CreateColumnarCapellaLinkRequest{
		LinkName:           linkName,
		ProvisionedCluster: capellacontrol.ProvisionedCluster{ClusterId: resolvedClusterId},
	}
	return d.mgr.Client.CreateColumnarCapellaLink(ctx, columnarInfo.Columnar.TenantID, columnarInfo.Columnar.ProjectID, columnarInfo.Columnar.ID, req)
}

func (d *Deployer) CreateS3Link(ctx context.Context, columnarID, linkName, region, endpoint, accessKey, secretKey string) error {
	columnarInfo, err := d.getCluster(ctx, columnarID)
	if err != nil {
		return err
	}
	if columnarInfo.Columnar == nil {
		return errors.Wrap(err, "this is not a columnar cluster")
	}

	req := &capellacontrol.CreateColumnarS3LinkRequest{
		Region:          region,
		AccessKeyId:     accessKey,
		SecretAccessKey: secretKey,
		SessionToken:    "",
		Endpoint:        endpoint,
		Type:            "s3",
	}
	return d.mgr.Client.CreateColumnarS3Link(ctx, columnarInfo.Columnar.TenantID, columnarInfo.Columnar.ProjectID, columnarInfo.Columnar.ID, linkName, req)
}

func (d *Deployer) DropLink(ctx context.Context, columnarID, linkName string) error {
	columnarInfo, err := d.getCluster(ctx, columnarID)
	if err != nil {
		return err
	}
	if columnarInfo.Columnar == nil {
		return errors.Wrap(err, "this is not a columnar cluster")
	}

	req := &capellacontrol.ColumnarQueryRequest{
		Statement:   fmt.Sprintf("DROP LINK `%s`", linkName),
		MaxWarnings: 25,
	}
	return d.mgr.Client.DoBasicColumnarQuery(ctx, columnarInfo.Columnar.TenantID, columnarInfo.Columnar.ProjectID, columnarInfo.Columnar.ID, req)
}

func (d *Deployer) EnableDataApi(ctx context.Context, clusterID string) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	cloudProjectID := clusterInfo.Cluster.Project.Id
	cloudClusterID := clusterInfo.Cluster.Id

	d.logger.Debug("enabling data API")

	err = d.client.EnableDataApi(ctx, d.tenantID, cloudProjectID, cloudClusterID)
	if err != nil {
		return errors.Wrap(err, "failed to enable Data API")
	}

	d.logger.Debug("waiting for Data API to enable")

	err = d.mgr.WaitForDataApiEnabled(ctx, d.tenantID, cloudClusterID)
	if err != nil {
		return errors.Wrap(err, "failed to wait for Data API enablement")
	}

	return nil
}

func (d *Deployer) GetGatewayCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("clouddeploy does not support getting gateway certificates")
}

func (d *Deployer) getMgmtX(ctx context.Context, clusterID string) (*cbmgmtx.Management, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	cloudProjectID := clusterInfo.Cluster.Project.Id
	cloudClusterID := clusterInfo.Cluster.Id

	mcli, err := d.client.GetMgmtX(ctx, d.tenantID, cloudProjectID, cloudClusterID)
	if err != nil {
		return nil, err
	}

	return mcli, nil
}

func (d *Deployer) getQueryX(ctx context.Context, clusterID string) (*cbqueryx.Query, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	cloudProjectID := clusterInfo.Cluster.Project.Id
	cloudClusterID := clusterInfo.Cluster.Id

	qcli, err := d.client.GetQueryX(ctx, d.tenantID, cloudProjectID, cloudClusterID)
	if err != nil {
		return nil, err
	}

	return qcli, nil
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
	mcli, err := d.getMgmtX(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get mgmt client")
	}

	manifest, err := mcli.GetCollectionManifest(ctx, &cbmgmtx.GetCollectionManifestOptions{
		BucketName: bucketName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch collection manifest")
	}

	var scopes []deployment.ScopeInfo
	for _, scope := range manifest.Scopes {
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
	mcli, err := d.getMgmtX(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get mgmt client")
	}

	_, err = mcli.CreateScope(ctx, &cbmgmtx.CreateScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create scope")
	}

	return nil
}

func (d *Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	mcli, err := d.getMgmtX(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get mgmt client")
	}

	_, err = mcli.CreateCollection(ctx, &cbmgmtx.CreateCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create collection")
	}

	return nil
}

func (d *Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	mcli, err := d.getMgmtX(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get mgmt client")
	}

	_, err = mcli.DeleteScope(ctx, &cbmgmtx.DeleteScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete scope")
	}

	return nil
}

func (d *Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	mcli, err := d.getMgmtX(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get mgmt client")
	}

	_, err = mcli.DeleteCollection(ctx, &cbmgmtx.DeleteCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
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

func (d *Deployer) PauseNode(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("clouddeploy does not support node pausing")
}

func (d *Deployer) UnpauseNode(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("clouddeploy does not support node pausing")
}
