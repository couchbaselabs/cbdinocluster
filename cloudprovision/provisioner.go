package cloudprovision

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/capellacontrol"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Provisioner struct {
	logger   *zap.Logger
	client   *capellacontrol.Controller
	mgr      *capellacontrol.Manager
	tenantID string
}

type NewProvisionerOptions struct {
	Logger   *zap.Logger
	Client   *capellacontrol.Controller
	TenantID string
}

func NewProvisioner(opts *NewProvisionerOptions) (*Provisioner, error) {
	return &Provisioner{
		logger: opts.Logger,
		client: opts.Client,
		mgr: &capellacontrol.Manager{
			Logger: opts.Logger,
			Client: opts.Client,
		},
		tenantID: opts.TenantID,
	}, nil
}

type clusterInfo struct {
	Meta        *ProjectNameMetaData
	Project     *capellacontrol.ProjectInfo
	Cluster     *capellacontrol.ClusterInfo
	IsCorrupted bool
}

func (p *Provisioner) listClusters(ctx context.Context) ([]*clusterInfo, error) {
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
		meta, err := ParseProjectNameMetaData(project.Data.Name)
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

func (p *Provisioner) getCluster(ctx context.Context, clusterID string) (*clusterInfo, error) {
	clusters, err := p.listClusters(ctx)
	if err != nil {
		return nil, err
	}

	var foundCluster *clusterInfo
	for _, cluster := range clusters {
		if cluster.Meta.ID == clusterID {
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

type ClusterInfo struct {
	ClusterID      string
	CloudProjectID string
	CloudClusterID string
	Region         string
	Expiry         time.Time
	State          string
}

func (p *Provisioner) ListClusters(ctx context.Context) ([]*ClusterInfo, error) {
	clusters, err := p.listClusters(ctx)
	if err != nil {
		return nil, err
	}

	var out []*ClusterInfo

	for _, cluster := range clusters {
		if cluster.IsCorrupted {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID,
				CloudProjectID: cluster.Project.ID,
				CloudClusterID: "",
				Region:         "",
				Expiry:         time.Time{},
				State:          "corrupted",
			})
			continue
		} else if cluster.Cluster == nil {
			out = append(out, &ClusterInfo{
				ClusterID:      cluster.Meta.ID,
				CloudProjectID: cluster.Project.ID,
				CloudClusterID: "",
				Region:         "",
				Expiry:         time.Time{},
				State:          "provisioning",
			})
			continue
		}

		out = append(out, &ClusterInfo{
			ClusterID:      cluster.Meta.ID,
			CloudProjectID: cluster.Project.ID,
			CloudClusterID: cluster.Cluster.Id,
			Region:         cluster.Cluster.Provider.Region,
			Expiry:         cluster.Meta.Expiry,
			State:          cluster.Cluster.Status.State,
		})
	}

	return out, nil
}

type NewClusterOptions struct {
	Expiry time.Duration
}

func (p *Provisioner) NewCluster(ctx context.Context, opts *NewClusterOptions) (*ClusterInfo, error) {
	clusterID := uuid.NewString()

	expiryTime := time.Now().Add(opts.Expiry)
	expiryTimeUnixSecs := expiryTime.Unix()

	projectName := fmt.Sprintf("cbdc2_%s_%d", clusterID, expiryTimeUnixSecs)

	p.logger.Debug("creating a new cloud project")

	newProject, err := p.client.CreateProject(ctx, p.tenantID, &capellacontrol.CreateProjectRequest{
		Name: projectName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create project")
	}

	cloudProjectID := newProject.Id

	p.logger.Debug("fetching deployment options project")

	deploymentOpts, err := p.client.GetDeploymentOptions(ctx, p.tenantID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get deployment options")
	}

	clusterName := fmt.Sprintf("cbdc2_%s", clusterID)

	p.logger.Debug("creating a new cloud cluster")

	newCluster, err := p.client.CreateCluster(ctx, p.tenantID, &capellacontrol.CreateClusterRequest{
		CIDR:        deploymentOpts.SuggestedCidr,
		Description: "",
		Name:        clusterName,
		Plan:        "Developer Pro",
		ProjectId:   cloudProjectID,
		Provider:    "aws",
		Region:      "us-west-2",
		Server:      deploymentOpts.ServerVersions.DefaultVersion,
		SingleAZ:    false,
		Specs: []capellacontrol.CreateClusterRequest_Spec{
			{
				Compute: "m5.xlarge",
				Count:   3,
				Disk: capellacontrol.CreateClusterRequest_Spec_Disk{
					Type:     "gp3",
					SizeInGb: 50,
					Iops:     3000,
				},
				DiskAutoScaling: capellacontrol.CreateClusterRequest_Spec_DiskScaling{
					Enabled: true,
				},
				Provider: "aws",
				Services: []string{"kv", "index", "n1ql", "fts"},
			},
		},
		Timezone: "PT",
	})
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
		if cluster.ClusterID == clusterID {
			thisCluster = cluster
		}
	}
	if thisCluster == nil {
		return nil, errors.New("failed to find new cluster after deployment")
	}

	return thisCluster, nil
}

func (p *Provisioner) removeCluster(ctx context.Context, clusterInfo *clusterInfo) error {
	p.logger.Debug("deleting the cloud cluster", zap.String("cluster-id", clusterInfo.Meta.ID))

	err := p.client.DeleteCluster(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id)
	if err != nil {
		return errors.Wrap(err, "failed to delete cluster")
	}

	p.logger.Debug("waiting for cluster deletion to finish")

	err = p.mgr.WaitForClusterState(ctx, p.tenantID, clusterInfo.Cluster.Id, "")
	if err != nil {
		return errors.Wrap(err, "failed to wait for cluster destruction")
	}

	p.logger.Debug("deleting the cloud project")

	err = p.client.DeleteProject(ctx, p.tenantID, clusterInfo.Cluster.Project.Id)
	if err != nil {
		return errors.Wrap(err, "failed to delete project")
	}

	return nil
}

func (p *Provisioner) RemoveCluster(ctx context.Context, clusterID string) error {
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

func (p *Provisioner) ListAllowListEntries(ctx context.Context, clusterID string) ([]*AllowListEntry, error) {
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

func (p *Provisioner) AddAllowListEntry(ctx context.Context, clusterID string, cidr string) error {
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

func (p *Provisioner) RemoveAllowListEntry(ctx context.Context, clusterID string, cidr string) error {
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

func (p *Provisioner) EnablePrivateEndpoints(ctx context.Context, clusterID string) error {
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

func (p *Provisioner) DisablePrivateEndpoints(ctx context.Context, clusterID string) error {
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

func (p *Provisioner) GetPrivateEndpointDetails(ctx context.Context, clusterID string) (*PrivateEndpointDetails, error) {
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

func (p *Provisioner) AcceptPrivateEndpointLink(ctx context.Context, clusterID string, vpceID string) error {
	clusterInfo, err := p.getCluster(ctx, clusterID)
	if err != nil {
		return err
	}

	_, err = p.mgr.WaitForPrivateEndpointLink(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, vpceID)
	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoint link")
	}

	err = p.client.AcceptPrivateEndpointLink(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, &capellacontrol.PrivateEndpointAcceptLinkRequest{
		EndpointID: vpceID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to accept private endpoint link")
	}

	err = p.mgr.WaitForPrivateEndpointLinkState(ctx, p.tenantID, clusterInfo.Cluster.Project.Id, clusterInfo.Cluster.Id, vpceID, "linked")
	if err != nil {
		return errors.Wrap(err, "failed to wait for private endpoint link to establish")
	}

	return nil
}

func (p *Provisioner) RemoveAll(ctx context.Context) error {
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

func (p *Provisioner) Cleanup(ctx context.Context) error {
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
				zap.String("cluster-id", cluster.Meta.ID))

			p.removeCluster(ctx, cluster)
		}
	}

	return nil
}
