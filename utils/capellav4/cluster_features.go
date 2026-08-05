package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

type getCertificateResponse struct {
	Certificate string `json:"certificate"`
}

// GetCertificate returns the cluster trust certificate. The v2 API returned a
// list of trusted CAs that callers had to filter; v4 returns the single relevant
// certificate.
func (c *Client) GetCertificate(ctx context.Context, orgID, projectID, clusterID string) (string, error) {
	resp := &getCertificateResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/certificates", orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return "", err
	}
	return resp.Certificate, nil
}

const (
	DataApiStateEnabled   = "enabled"
	DataApiStateDisabled  = "disabled"
	DataApiStateEnabling  = "enabling"
	DataApiStateDisabling = "disabling"
)

type DataApiInfo struct {
	Enabled                  bool   `json:"enabled"`
	State                    string `json:"state"`
	EnabledForNetworkPeering bool   `json:"enabledForNetworkPeering"`
	StateForNetworkPeering   string `json:"stateForNetworkPeering"`
	ConnectionString         string `json:"connectionString"`
}

func (c *Client) GetDataApi(ctx context.Context, orgID, projectID, clusterID string) (*DataApiInfo, error) {
	resp := &DataApiInfo{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/dataAPI", orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

type UpdateDataApiRequest struct {
	EnableDataApi        bool `json:"enableDataApi"`
	EnableNetworkPeering bool `json:"enableNetworkPeering"`
}

func (c *Client) UpdateDataApi(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *UpdateDataApiRequest,
) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/dataAPI", orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodPut, path, req, nil)
}

// Private endpoint service states.
const (
	PrivateEndpointServiceEnabled  = "enabled"
	PrivateEndpointServiceDisabled = "disabled"
	PrivateEndpointServiceIdle     = "idle"
)

// Private endpoint connection states.
const (
	PrivateEndpointPending           = "pending"
	PrivateEndpointPendingAcceptance = "pendingAcceptance"
	PrivateEndpointLinked            = "linked"
	PrivateEndpointRejected          = "rejected"
	PrivateEndpointFailed            = "failed"
)

type PrivateEndpointServiceInfo struct {
	Enabled bool `json:"enabled"`
	// ServiceName is what the cloud provider CLI needs to create the endpoint.
	ServiceName string `json:"serviceName"`
	Status      string `json:"status"`
}

func (c *Client) GetPrivateEndpointService(ctx context.Context, orgID, projectID, clusterID string) (*PrivateEndpointServiceInfo, error) {
	resp := &PrivateEndpointServiceInfo{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService",
		orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) EnablePrivateEndpointService(ctx context.Context, orgID, projectID, clusterID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService",
		orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodPost, path, nil, nil)
}

func (c *Client) DisablePrivateEndpointService(ctx context.Context, orgID, projectID, clusterID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService",
		orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}

type PrivateEndpointInfo struct {
	ID          string `json:"id"`
	ServiceName string `json:"serviceName"`
	Status      string `json:"status"`
}

type ListPrivateEndpointsResponse struct {
	// PrivateEndpointDNS was reported by the v2 privateendpoint/details call.
	PrivateEndpointDNS string                 `json:"privateEndpointDNS"`
	Endpoints          []*PrivateEndpointInfo `json:"endpoints"`
}

func (c *Client) ListPrivateEndpoints(ctx context.Context, orgID, projectID, clusterID string) (*ListPrivateEndpointsResponse, error) {
	resp := &ListPrivateEndpointsResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService/endpoints",
		orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// AcceptPrivateEndpoint associates a pending endpoint request with the service,
// which is what makes the endpoint usable.
func (c *Client) AcceptPrivateEndpoint(ctx context.Context, orgID, projectID, clusterID, endpointID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService/endpoints/%s/associate",
		orgID, projectID, clusterID, endpointID)
	return c.doWrite(ctx, http.MethodPost, path, nil, nil)
}

// EndpointCommandRequest carries the provider specific fields for generating the
// endpoint creation command. Only the fields for the cluster's own provider are
// populated; the rest are omitted.
type EndpointCommandRequest struct {
	// AWS.
	VpcID string `json:"vpcID,omitempty"`
	// AWS and GCP.
	SubnetIDs []string `json:"subnetIDs,omitempty"`
	// GCP.
	VpcNetworkID string `json:"vpcNetworkID,omitempty"`
	// Azure.
	ResourceGroupName string `json:"resourceGroupName,omitempty"`
	VirtualNetwork    string `json:"virtualNetwork,omitempty"`
}

type EndpointCommandResponse struct {
	Command string `json:"command"`
}

// GetPrivateEndpointCommand returns the provider CLI command or script that
// creates the private endpoint on the caller's side.
func (c *Client) GetPrivateEndpointCommand(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *EndpointCommandRequest,
) (*EndpointCommandResponse, error) {
	resp := &EndpointCommandResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService/endpointCommand",
		orgID, projectID, clusterID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
