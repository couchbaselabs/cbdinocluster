package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

const OrganizationRoleOwner = "organizationOwner"

// ExpiryNever keeps a key valid until it is deleted.
const ExpiryNever = -1

type ApiKeyInfo struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Expiry            float64  `json:"expiry"`
	AllowedCIDRs      []string `json:"allowedCIDRs"`
	OrganizationRoles []string `json:"organizationRoles"`
	Audit             Audit    `json:"audit"`
}

func (c *Client) ListApiKeys(ctx context.Context, orgID string) ([]*ApiKeyInfo, error) {
	path := fmt.Sprintf("/v4/organizations/%s/apikeys", orgID)
	return listAll[*ApiKeyInfo](ctx, c, path, nil)
}

type CreateApiKeyRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Days until the key expires, or ExpiryNever. Omitting it means 180 days,
	// so callers that want a lasting key must set it.
	Expiry            float64  `json:"expiry,omitempty"`
	OrganizationRoles []string `json:"organizationRoles"`
}

type CreateApiKeyResponse struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

// Token is the bearer secret and Capella returns it here only. A caller that
// loses it can only rotate the key to get a new one.
func (c *Client) CreateApiKey(
	ctx context.Context,
	orgID string,
	req *CreateApiKeyRequest,
) (*CreateApiKeyResponse, error) {
	resp := &CreateApiKeyResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/apikeys", orgID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) DeleteApiKey(ctx context.Context, orgID, keyID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/apikeys/%s", orgID, keyID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}

type RotateApiKeyResponse struct {
	SecretKey string `json:"secretKey"`
	Token     string `json:"token"`
}

// Rotation replaces the bearer secret, so any client still holding the old
// token stops working right away.
func (c *Client) RotateApiKey(
	ctx context.Context,
	orgID, keyID string,
) (*RotateApiKeyResponse, error) {
	resp := &RotateApiKeyResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/apikeys/%s/rotate", orgID, keyID)
	if err := c.doWrite(ctx, http.MethodPost, path, nil, resp); err != nil {
		return nil, err
	}
	return resp, nil
}
