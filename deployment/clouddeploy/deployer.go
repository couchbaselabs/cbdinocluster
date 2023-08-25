package clouddeploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Deployer struct {
	logger        *zap.Logger
	client        *capellacontrol.Controller
	mgr           *capellacontrol.Manager
	tenantID      string
	defaultCloud  string
	defaultRegion string
}

var _ deployment.Deployer = (*Deployer)(nil)

type NewDeployerOptions struct {
	Logger        *zap.Logger
	Client        *capellacontrol.Controller
	TenantID      string
	DefaultCloud  string
	DefaultRegion string
}

func NewDeployer(opts *NewDeployerOptions) (*Deployer, error) {
	return &Deployer{
		logger: opts.Logger,
		client: opts.Client,
		mgr: &capellacontrol.Manager{
			Logger: opts.Logger,
			Client: opts.Client,
		},
		tenantID:      opts.TenantID,
		defaultCloud:  opts.DefaultCloud,
		defaultRegion: opts.DefaultRegion,
	}, nil
}

type clusterInfo struct {
	Meta        *stringclustermeta.MetaData
	Project     *capellacontrol.ProjectInfo
	Cluster     *capellacontrol.ClusterInfo
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
			out = append(out, &clusterInfo{
				Meta:        meta,
				Project:     project.Data,
				Cluster:     nil,
				IsCorrupted: false,
			})
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
				CloudProjectID: cluster.Project.ID,
				CloudClusterID: "",
				Region:         "",
				Expiry:         cluster.Meta.Expiry,
				State:          "corrupted",
			})
			continue
		} else if cluster.Cluster == nil {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID.String(),
				CloudProjectID: cluster.Project.ID,
				CloudClusterID: "",
				Region:         "",
				Expiry:         cluster.Meta.Expiry,
				State:          "provisioning",
			})
			continue
		}

		out = append(out, &ClusterInfo{
			ClusterID:      cluster.Meta.ID.String(),
			CloudProjectID: cluster.Project.ID,
			CloudClusterID: cluster.Cluster.Id,
			Region:         cluster.Cluster.Provider.Region,
			Expiry:         cluster.Meta.Expiry,
			State:          cluster.Cluster.Status.State,
		})
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

func (p *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	var clusterVersion string
	for _, nodeGroup := range def.NodeGroups {
		if clusterVersion == "" {
			clusterVersion = nodeGroup.Version
		} else {
			if clusterVersion != nodeGroup.Version {
				return nil, errors.New("all node groups must have the same version")
			}
		}
	}

	clusterID := cbdcuuid.New()

	metaData := stringclustermeta.MetaData{
		ID:     clusterID,
		Expiry: time.Now().Add(def.Expiry),
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

	cloudProvider := p.defaultCloud
	cloudRegion := p.defaultRegion
	clusterCidr := ""

	if def.CloudCluster != nil {
		if def.CloudCluster.CloudProvider != "" {
			cloudProvider = def.CloudCluster.CloudProvider
		}
		if def.CloudCluster.Region != "" {
			cloudRegion = def.CloudCluster.Region
		}
		if def.CloudCluster.Cidr != "" {
			clusterCidr = def.CloudCluster.Cidr
		}
	}

	deploymentProvider := ""
	clusterProvider := ""
	nodeProvider := ""
	if cloudProvider == "aws" {
		deploymentProvider = "aws"
		clusterProvider = "aws"
		nodeProvider = "aws"
	} else if cloudProvider == "gcp" {
		deploymentProvider = "gcp"
		clusterProvider = "gcp"
		nodeProvider = "gcp"
	} else if cloudProvider == "azure" {
		deploymentProvider = "azure"
		clusterProvider = "hostedAzure"
		nodeProvider = "azure"
	} else {
		return nil, errors.New("invalid cloud provider specified")
	}

	p.logger.Debug("fetching deployment options project")

	deploymentOpts, err := p.client.GetProviderDeploymentOptions(ctx, p.tenantID, &capellacontrol.GetProviderDeploymentOptionsRequest{
		Provider: deploymentProvider,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get deployment options")
	}

	if clusterVersion == "" {
		clusterVersion = deploymentOpts.ServerVersions.DefaultVersion
	}
	if clusterCidr == "" {
		clusterCidr = deploymentOpts.SuggestedCidr
	}

	p.logger.Debug("creating a new cloud cluster")

	clusterName := fmt.Sprintf("cbdc2_%s", clusterID)

	var specs []capellacontrol.CreateClusterRequest_Spec
	for _, nodeGroup := range def.NodeGroups {
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
			// add defaults for gcp provider
			instanceType = ""
			diskType = ""
			diskSize = 0
			diskIops = 0
		} else if cloudProvider == "azure" {
			instanceType = "Standard_D4s_v5"
			diskType = "P6"
			diskSize = 64
			diskIops = 240
		} else {
			return nil, errors.New("invalid cloud provider specified")
		}

		services := nodeGroup.Services
		if len(services) == 0 {
			services = []clusterdef.Service{
				clusterdef.KvService,
				clusterdef.IndexService,
				clusterdef.QueryService,
				clusterdef.SearchService,
			}
		}

		nsServices, err := clusterdef.ServicesToNsServices(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

		diskAutoScalingEnabled := true
		if clusterVersion == "7.1" {
			diskAutoScalingEnabled = false
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
				Enabled: diskAutoScalingEnabled,
			},
			Provider: nodeProvider,
			Services: nsServices,
		})
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

	cloudClusterID := newCluster.Id

	p.logger.Debug("waiting for cluster creation to complete")

	err = p.mgr.WaitForClusterState(ctx, p.tenantID, cloudClusterID, "healthy")
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

func (p *Deployer) removeCluster(ctx context.Context, clusterInfo *clusterInfo) error {
	p.logger.Debug("deleting the cloud cluster", zap.String("cluster-id", clusterInfo.Meta.ID.String()))

	if clusterInfo.Cluster != nil {
		err := p.client.DeleteCluster(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
		if err != nil {
			return errors.Wrap(err, "failed to delete cluster")
		}

		p.logger.Debug("waiting for cluster deletion to finish")

		err = p.mgr.WaitForClusterState(ctx, p.tenantID, clusterInfo.Cluster.Id, "")
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

	entries, err := p.client.ListAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       1000,
		SortBy:        "name",
		SortDirection: "asc",
	})
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

	err = p.client.UpdateAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.UpdateAllowListEntriesRequest{
		Create: []capellacontrol.UpdateAllowListEntriesRequest_Entry{
			{
				Cidr:    cidr,
				Comment: "",
			},
		},
	})
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

	entries, err := p.client.ListAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       1000,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return errors.Wrap(err, "failed to fetch allow list entries")
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

	err = p.client.UpdateAllowListEntries(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.UpdateAllowListEntriesRequest{
		Delete: []string{foundEntryId},
	})
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

	err = p.client.EnablePrivateEndpoints(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	if err != nil {
		return errors.Wrap(err, "failed to enable private endpoints")
	}

	err = p.mgr.WaitForPrivateEndpointsEnabled(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
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

	return p.client.DisablePrivateEndpoints(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
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
}

func (p *Deployer) AcceptPrivateEndpointLink(ctx context.Context, clusterID string, endpointID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	// in some deployment scenarios, the endpoint-id that the user has is only the
	// first part of the id, and the rest of the id comes from somewhere else, so we
	// list all of the ids, and pick the one that matches.
	peLinks, err := p.mgr.Client.ListPrivateEndpointLinks(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	if err != nil {
		return errors.Wrap(err, "failed to list private endpoint links")
	}

	fullEndpointId := ""
	for _, peLink := range peLinks.Data {
		if strings.Contains(peLink.EndpointID, endpointID) {
			fullEndpointId = peLink.EndpointID
			break
		}
	}

	if fullEndpointId == "" {
		return fmt.Errorf("failed to identify endpoint '%s'", endpointID)
	}

	_, err = p.mgr.WaitForPrivateEndpointLink(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, fullEndpointId)
	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoint link")
	}

	err = p.client.AcceptPrivateEndpointLink(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PrivateEndpointAcceptLinkRequest{
		EndpointID: fullEndpointId,
	})
	if err != nil {
		return errors.Wrap(err, "failed to accept private endpoint link")
	}

	err = p.mgr.WaitForPrivateEndpointLinkState(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, fullEndpointId, "linked")
	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoint link to establish")
	}

	return nil
}

func (p *Deployer) RemoveAll(ctx context.Context) error {
	clusters, err := p.client.ListAllClusters(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return errors.Wrap(err, "failed to list all clusters")
	}

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
			return errors.Wrap(err, "failed to remove cluster")
		}
	}

	for _, cluster := range clustersToRemove {
		p.logger.Info("waiting for cluster removal to complete", zap.String("cluster-id", cluster.Id))

		err := p.mgr.WaitForClusterState(ctx, p.tenantID, cluster.Id, "")
		if err != nil {
			return errors.Wrap(err, "failed to wait for cluster removal to finish")
		}
	}

	projects, err := p.client.ListProjects(ctx, p.tenantID, &capellacontrol.PaginatedRequest{
		Page:          1,
		PerPage:       100,
		SortBy:        "name",
		SortDirection: "asc",
	})
	if err != nil {
		return errors.Wrap(err, "failed to list all projects")
	}

	var projectsToRemove []*capellacontrol.ProjectInfo
	for _, project := range projects.Data {
		if !strings.HasPrefix(project.Data.Name, "cbdc2_") {
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
			return errors.Wrap(err, "failed to remove project")
		}
	}

	return nil
}

func (p *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	return &deployment.ConnectInfo{
		ConnStr: "",
		Mgmt:    "",
	}, nil
}

func (p *Deployer) Cleanup(ctx context.Context) error {
	// we just use our own commands to do this easily...
	clusters, err := p.listClusters(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list clusters")
	}

	curTime := time.Now()
	for _, cluster := range clusters {
		if cluster.Cluster == nil {
			continue
		}

		if !cluster.Meta.Expiry.After(curTime) {
			// in order to avoid blocking people trying to do work while
			// we cleanup old clusters, we only delete the cluster here
			p.logger.Info("removing cluster",
				zap.String("cluster-id", cluster.Meta.ID.String()))

			p.removeCluster(ctx, cluster)
		}
	}

	return nil
}
