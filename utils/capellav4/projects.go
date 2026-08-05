package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

type Audit struct {
	CreatedBy  string `json:"createdBy"`
	CreatedAt  string `json:"createdAt"`
	ModifiedBy string `json:"modifiedBy"`
	ModifiedAt string `json:"modifiedAt"`
	Version    int    `json:"version"`
}

type ProjectInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Audit       Audit  `json:"audit"`
}

// ListProjects returns every project in the organization. The v4 project object
// carries no cluster count, so callers that need one must count clusters
// themselves.
func (c *Client) ListProjects(ctx context.Context, orgID string) ([]*ProjectInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/projects", orgID)
	return listAll[*ProjectInfo](ctx, c, path, nil)
}

type CreateProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type CreateProjectResponse struct {
	ID string `json:"id"`
}

func (c *Client) CreateProject(
	ctx context.Context,
	orgID string,
	req *CreateProjectRequest,
) (*CreateProjectResponse, error) {
	resp := &CreateProjectResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects", orgID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

type UpdateProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateProject replaces the project name and description. cbdinocluster stores
// the cluster ID and expiry time in the project name, so this is how a cluster's
// expiry is extended.
func (c *Client) UpdateProject(
	ctx context.Context,
	orgID, projectID string,
	req *UpdateProjectRequest,
) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s", orgID, projectID)
	return c.doWrite(ctx, http.MethodPut, path, req, nil)
}

func (c *Client) DeleteProject(ctx context.Context, orgID, projectID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s", orgID, projectID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}
