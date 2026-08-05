package capellav4

import (
	"context"
	"fmt"
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
