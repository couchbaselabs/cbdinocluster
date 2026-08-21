package capellav4

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// The openapi-capella-v4.json spec declares the analyticsClusters paths with no
// operations. These fields come from the live API.
type AnalyticsClusterInfo struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	CloudProvider string       `json:"cloudProvider"`
	Region        string       `json:"region"`
	Nodes         int          `json:"nodes"`
	Compute       Compute      `json:"compute"`
	Availability  Availability `json:"availability"`
	Support       Support      `json:"support"`
	CurrentState  string       `json:"currentState"`
}

// The analytics API reports the provider in upper case, unlike the rest of v4.
func (a *AnalyticsClusterInfo) CloudProviderName() string {
	return strings.ToLower(a.CloudProvider)
}

func (c *Client) ListAnalyticsClusters(ctx context.Context, orgID, projectID string) ([]*AnalyticsClusterInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters", orgID, projectID)
	return listAll[*AnalyticsClusterInfo](ctx, c, path, nil)
}

// The response carries no project reference, verified against the live API, so
// the result cannot be attributed to projects and this cannot replace the per
// project listing. Callers that need the project must use ListAnalyticsClusters.
func (c *Client) ListAllAnalyticsClusters(ctx context.Context, orgID string) ([]*AnalyticsClusterInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/analyticsClusters", orgID)
	return listAll[*AnalyticsClusterInfo](ctx, c, path, nil)
}

// The analytics API reports the private DNS name on the service. The
// provisioned cluster API reports it with the endpoint list.
type AnalyticsPrivateEndpointServiceInfo struct {
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
	ServiceName string `json:"serviceName"`
	PrivateDNS  string `json:"privateDns"`
}

func (c *Client) GetAnalyticsPrivateEndpointService(ctx context.Context, orgID, projectID, clusterID string) (*AnalyticsPrivateEndpointServiceInfo, error) {
	resp := &AnalyticsPrivateEndpointServiceInfo{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/privateEndpointService",
		orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) EnableAnalyticsPrivateEndpointService(ctx context.Context, orgID, projectID, clusterID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/privateEndpointService",
		orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodPost, path, nil, nil)
}

func (c *Client) DisableAnalyticsPrivateEndpointService(ctx context.Context, orgID, projectID, clusterID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/privateEndpointService",
		orgID, projectID, clusterID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}

// The analytics API reports the endpoint ID as endpointId (instead of id)
type analyticsPrivateEndpointInfo struct {
	EndpointID string `json:"endpointId"`
	Status     string `json:"status"`
}

type listAnalyticsPrivateEndpointsResponse struct {
	Endpoints []*analyticsPrivateEndpointInfo `json:"endpoints"`
}

func (c *Client) ListAnalyticsPrivateEndpoints(ctx context.Context, orgID, projectID, clusterID string) ([]*PrivateEndpointInfo, error) {
	resp := &listAnalyticsPrivateEndpointsResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/privateEndpointService/endpoints",
		orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}

	endpoints := make([]*PrivateEndpointInfo, 0, len(resp.Endpoints))
	for _, endpoint := range resp.Endpoints {
		endpoints = append(endpoints, &PrivateEndpointInfo{
			ID:     endpoint.EndpointID,
			Status: endpoint.Status,
		})
	}
	return endpoints, nil
}

func (c *Client) AcceptAnalyticsPrivateEndpoint(ctx context.Context, orgID, projectID, clusterID, endpointID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/privateEndpointService/endpoints/%s/associate",
		orgID, projectID, clusterID, endpointID)
	return c.doWrite(ctx, http.MethodPost, path, nil, nil)
}
