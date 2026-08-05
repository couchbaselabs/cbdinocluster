package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

// Database credential privileges. The v4 API also accepts read and write as
// aliases of these two.
const (
	PrivilegeDataReader = "data_reader"
	PrivilegeDataWriter = "data_writer"
)

type UserScope struct {
	Name        string   `json:"name"`
	Collections []string `json:"collections,omitempty"`
}

type UserBucket struct {
	Name   string      `json:"name"`
	Scopes []UserScope `json:"scopes,omitempty"`
}

type UserResources struct {
	Buckets []UserBucket `json:"buckets,omitempty"`
}

// UserAccess grants a set of privileges. An absent Resources grants access to
// every bucket.
type UserAccess struct {
	Privileges []string       `json:"privileges"`
	Resources  *UserResources `json:"resources,omitempty"`
}

type UserInfo struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Access []UserAccess `json:"access"`
	Audit  Audit        `json:"audit"`
}

// HasPrivilege reports whether the credential holds a privilege, accounting for
// the read and write aliases the API accepts.
func (u *UserInfo) HasPrivilege(privilege string) bool {
	var alias string
	switch privilege {
	case PrivilegeDataReader:
		alias = "read"
	case PrivilegeDataWriter:
		alias = "write"
	}

	for _, access := range u.Access {
		for _, granted := range access.Privileges {
			if granted == privilege || (alias != "" && granted == alias) {
				return true
			}
		}
	}
	return false
}

func (c *Client) ListUsers(ctx context.Context, orgID, projectID, clusterID string) ([]*UserInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/users", orgID, projectID, clusterID)
	return listAll[*UserInfo](ctx, c, path, nil)
}

type CreateUserRequest struct {
	Name string `json:"name"`
	// Password may be empty, in which case Capella generates one and returns it.
	Password string       `json:"password,omitempty"`
	Access   []UserAccess `json:"access"`
}

type CreateUserResponse struct {
	ID       string `json:"id"`
	Password string `json:"password"`
}

func (c *Client) CreateUser(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *CreateUserRequest,
) (*CreateUserResponse, error) {
	resp := &CreateUserResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/users", orgID, projectID, clusterID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) DeleteUser(ctx context.Context, orgID, projectID, clusterID, userID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/users/%s",
		orgID, projectID, clusterID, userID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}
