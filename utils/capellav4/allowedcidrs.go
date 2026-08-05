package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

type AllowedCidrInfo struct {
	ID        string `json:"id"`
	Cidr      string `json:"cidr"`
	Comment   string `json:"comment"`
	ExpiresAt string `json:"expiresAt"`
	Status    string `json:"status"`
	Type      string `json:"type"`
	Audit     Audit  `json:"audit"`
}

func clusterCidrsPath(orgID, projectID, clusterID string) string {
	return fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/allowedcidrs",
		orgID, projectID, clusterID)
}

func analyticsCidrsPath(orgID, projectID, clusterID string) string {
	return fmt.Sprintf("/v4/organizations/%s/projects/%s/analyticsClusters/%s/allowedcidrs",
		orgID, projectID, clusterID)
}

func (c *Client) ListAllowedCidrs(ctx context.Context, orgID, projectID, clusterID string) ([]*AllowedCidrInfo, error) {
	return listAll[*AllowedCidrInfo](ctx, c, clusterCidrsPath(orgID, projectID, clusterID), nil)
}

// ListAnalyticsAllowedCidrs reads the allow list of an analytics cluster. Allowed
// CIDRs are one of the few analytics features that v4 covers, so this avoids a v2
// session for columnar allow list commands.
func (c *Client) ListAnalyticsAllowedCidrs(ctx context.Context, orgID, projectID, clusterID string) ([]*AllowedCidrInfo, error) {
	return listAll[*AllowedCidrInfo](ctx, c, analyticsCidrsPath(orgID, projectID, clusterID), nil)
}

type CreateAllowedCidrRequest struct {
	Cidr      string `json:"cidr"`
	Comment   string `json:"comment,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

type CreateAllowedCidrResponse struct {
	ID string `json:"id"`
}

// CreateAllowedCidr adds one CIDR to the cluster allow list. The v2 API had a
// bulk endpoint that created and deleted in a single call; v4 requires one
// request per entry.
func (c *Client) CreateAllowedCidr(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *CreateAllowedCidrRequest,
) (*CreateAllowedCidrResponse, error) {
	return c.createAllowedCidr(ctx, clusterCidrsPath(orgID, projectID, clusterID), req)
}

func (c *Client) CreateAnalyticsAllowedCidr(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *CreateAllowedCidrRequest,
) (*CreateAllowedCidrResponse, error) {
	return c.createAllowedCidr(ctx, analyticsCidrsPath(orgID, projectID, clusterID), req)
}

func (c *Client) createAllowedCidr(
	ctx context.Context,
	path string,
	req *CreateAllowedCidrRequest,
) (*CreateAllowedCidrResponse, error) {
	resp := &CreateAllowedCidrResponse{}
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) DeleteAllowedCidr(ctx context.Context, orgID, projectID, clusterID, cidrID string) error {
	path := fmt.Sprintf("%s/%s", clusterCidrsPath(orgID, projectID, clusterID), cidrID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) DeleteAnalyticsAllowedCidr(ctx context.Context, orgID, projectID, clusterID, cidrID string) error {
	path := fmt.Sprintf("%s/%s", analyticsCidrsPath(orgID, projectID, clusterID), cidrID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}
