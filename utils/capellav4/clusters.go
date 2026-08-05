package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

// Cluster states reported in ClusterInfo.CurrentState.
const (
	StateDeploying   = "deploying"
	StateHealthy     = "healthy"
	StateDegraded    = "degraded"
	StateScaling     = "scaling"
	StateUpgrading   = "upgrading"
	StateRebalancing = "rebalancing"
	StatePeering     = "peering"
	StateDestroying  = "destroying"
	StateTurnedOff   = "turnedOff"
	StateTurningOff  = "turningOff"
	StateTurningOn   = "turningOn"
	StateDraft       = "draft"
)

// Cloud provider identifiers. The v4 API uses plain names, unlike the v2 API
// which used hostedAWS, hostedGCP and hostedAzure.
const (
	ProviderAws   = "aws"
	ProviderGcp   = "gcp"
	ProviderAzure = "azure"
)

const (
	AvailabilitySingle = "single"
	AvailabilityMulti  = "multi"
)

const (
	ConfigurationTypeSingleNode = "singleNode"
	ConfigurationTypeMultiNode  = "multiNode"
)

type CloudProvider struct {
	Type   string `json:"type"`
	Region string `json:"region"`
	// Cidr may be omitted on create, in which case Capella allocates one.
	Cidr string `json:"cidr,omitempty"`
}

type CouchbaseServer struct {
	Version string `json:"version,omitempty"`
}

type Compute struct {
	Cpu int `json:"cpu"`
	Ram int `json:"ram"`
}

// Disk covers all three provider disk variants. The v4 schema models them as a
// one-of, but they differ only in which fields are present, so omitted fields
// are enough to select the right variant.
//
// AutoExpansion is only honoured for Azure. AWS and GCP accept the field on
// create but do not report it back, so do not rely on it there.
type Disk struct {
	Type          string `json:"type"`
	Storage       int    `json:"storage,omitempty"`
	Iops          int    `json:"iops,omitempty"`
	AutoExpansion bool   `json:"autoExpansion,omitempty"`
}

type Node struct {
	Compute Compute `json:"compute"`
	Disk    Disk    `json:"disk"`
}

type ServiceGroup struct {
	Node       Node     `json:"node"`
	NumOfNodes int      `json:"numOfNodes"`
	Services   []string `json:"services"`
}

type Availability struct {
	Type string `json:"type"`
}

type Support struct {
	Plan     string `json:"plan"`
	Timezone string `json:"timezone,omitempty"`
}

type ClusterInfo struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	ConfigurationType string          `json:"configurationType"`
	ConnectionString  string          `json:"connectionString"`
	CloudProvider     CloudProvider   `json:"cloudProvider"`
	CouchbaseServer   CouchbaseServer `json:"couchbaseServer"`
	ServiceGroups     []ServiceGroup  `json:"serviceGroups"`
	Availability      Availability    `json:"availability"`
	Support           Support         `json:"support"`
	CurrentState      string          `json:"currentState"`
	AppServiceID      string          `json:"appServiceId"`
	CmekID            string          `json:"cmekId"`
	Audit             Audit           `json:"audit"`
}

// ListClusters returns the clusters in a single project. The v4 API has no
// organization-wide cluster listing, so discovering every cluster means calling
// this once per project.
func (c *Client) ListClusters(ctx context.Context, orgID, projectID string) ([]*ClusterInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters", orgID, projectID)
	return listAll[*ClusterInfo](ctx, c, path, nil)
}

// GetCluster fetches one cluster. Prefer this over ListClusters when polling for
// a state change, since it avoids a request per project on every poll.
func (c *Client) GetCluster(ctx context.Context, orgID, projectID, clusterID string) (*ClusterInfo, error) {
	resp := &ClusterInfo{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s", orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

type CreateClusterRequest struct {
	Name              string        `json:"name"`
	Description       string        `json:"description,omitempty"`
	ConfigurationType string        `json:"configurationType,omitempty"`
	CloudProvider     CloudProvider `json:"cloudProvider"`
	// CouchbaseServer is a pointer so that an unset version is omitted from the
	// request entirely rather than being sent as an empty object.
	CouchbaseServer *CouchbaseServer `json:"couchbaseServer,omitempty"`
	ServiceGroups   []ServiceGroup   `json:"serviceGroups"`
	Availability    Availability     `json:"availability"`
	Support         Support          `json:"support"`
	Zones           []string         `json:"zones,omitempty"`
}

type CreateClusterResponse struct {
	ID string `json:"id"`
}

// CreateCluster provisions a cluster. Leaving CouchbaseServer.Version and
// CloudProvider.Cidr empty makes Capella choose the current default server
// version and allocate a free CIDR block, which is what replaces the v2
// deployment-options lookup.
func (c *Client) CreateCluster(
	ctx context.Context,
	orgID, projectID string,
	req *CreateClusterRequest,
) (*CreateClusterResponse, error) {
	resp := &CreateClusterResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters", orgID, projectID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

type CreateFreeTierClusterRequest struct {
	Name          string        `json:"name"`
	Description   string        `json:"description,omitempty"`
	CloudProvider CloudProvider `json:"cloudProvider"`
}

// CreateFreeTierCluster provisions a free tier cluster. Free tier clusters have
// a fixed topology, so no service groups or support plan are accepted.
func (c *Client) CreateFreeTierCluster(
	ctx context.Context,
	orgID, projectID string,
	req *CreateFreeTierClusterRequest,
) (*CreateClusterResponse, error) {
	resp := &CreateClusterResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/freeTier", orgID, projectID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

type UpdateClusterRequest struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Support       Support        `json:"support"`
	ServiceGroups []ServiceGroup `json:"serviceGroups"`
}

// UpdateCluster rescales a cluster. The v4 update body has no couchbaseServer
// field, so it cannot change the server version.
func (c *Client) UpdateCluster(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *UpdateClusterRequest,
) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s", orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodPut, path, req, nil)
}

func (c *Client) DeleteCluster(ctx context.Context, orgID, projectID, clusterID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s", orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}
