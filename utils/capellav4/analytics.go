package capellav4

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// AnalyticsClusterInfo describes an analytics cluster, the product previously
// called Columnar.
//
// Note that the shape differs from ClusterInfo: cloudProvider is a bare string
// rather than an object, and region sits at the top level. The provider is also
// reported in upper case, so use CloudProviderName to compare it against the
// lower case identifiers used elsewhere.
//
// The supplied openapi-capella-v4.json declares the analyticsClusters paths with
// no operations, so these fields were confirmed against the live API rather than
// generated from the spec.
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

// CloudProviderName returns the provider in the lower case form used by the
// provisioned cluster API and by cbdinocluster itself.
func (a *AnalyticsClusterInfo) CloudProviderName() string {
	return strings.ToLower(a.CloudProvider)
}

// ListAnalyticsClusters returns the analytics clusters in a single project.
//
// This is discovery only. The v4 API exposes no database credentials, no
// certificate and no connection string for analytics clusters, so those
// operations still need the internal v2 API.
func (c *Client) ListAnalyticsClusters(ctx context.Context, orgID, projectID string) ([]*AnalyticsClusterInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters", orgID, projectID)
	return listAll[*AnalyticsClusterInfo](ctx, c, path, nil)
}

// ListAllAnalyticsClusters returns every analytics cluster in the organization.
// The response carries no project ID, so prefer ListAnalyticsClusters when the
// caller needs to associate clusters with projects.
func (c *Client) ListAllAnalyticsClusters(ctx context.Context, orgID string) ([]*AnalyticsClusterInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/analyticsClusters", orgID)
	return listAll[*AnalyticsClusterInfo](ctx, c, path, nil)
}

// AnalyticsPrivateEndpointServiceInfo describes the private endpoint service of
// an analytics cluster. Unlike the provisioned cluster API, which reports the
// private DNS name with the endpoint list, the analytics API reports it on the
// service itself.
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

// The analytics API reports the endpoint ID as endpointId rather than id, so the
// endpoints are normalized to the shared PrivateEndpointInfo shape. The status
// values match the provisioned cluster ones.
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

// AcceptAnalyticsPrivateEndpoint associates a pending endpoint request with the
// service, which is what makes the endpoint usable.
func (c *Client) AcceptAnalyticsPrivateEndpoint(ctx context.Context, orgID, projectID, clusterID, endpointID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/privateEndpointService/endpoints/%s/associate",
		orgID, projectID, clusterID, endpointID)
	return c.doWrite(ctx, http.MethodPost, path, nil, nil)
}
