package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

type getCertificateResponse struct {
	Certificate string `json:"certificate"`
}

func (c *Client) GetCertificate(ctx context.Context, orgID, projectID, clusterID string) (string, error) {
	resp := &getCertificateResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/certificates", orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return "", err
	}
	return resp.Certificate, nil
}

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

const (
	PrivateEndpointServiceEnabled  = "enabled"
	PrivateEndpointServiceDisabled = "disabled"
	PrivateEndpointServiceIdle     = "idle"
)

const (
	PrivateEndpointPending           = "pending"
	PrivateEndpointPendingAcceptance = "pendingAcceptance"
	PrivateEndpointLinked            = "linked"
	PrivateEndpointRejected          = "rejected"
	PrivateEndpointFailed            = "failed"
)

type PrivateEndpointServiceInfo struct {
	Enabled     bool   `json:"enabled"`
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

func (c *Client) AcceptPrivateEndpoint(ctx context.Context, orgID, projectID, clusterID, endpointID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/privateEndpointService/endpoints/%s/associate",
		orgID, projectID, clusterID, endpointID)
	return c.doWrite(ctx, http.MethodPost, path, nil, nil)
}

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
