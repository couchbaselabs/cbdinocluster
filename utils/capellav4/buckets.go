package capellav4

import (
	"context"
	"fmt"
	"net/http"
)

const (
	BucketTypeCouchbase = "couchbase"
	BucketTypeEphemeral = "ephemeral"
)

const (
	StorageBackendCouchstore = "couchstore"
	StorageBackendMagma      = "magma"
)

type BucketInfo struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Type                     string `json:"type"`
	StorageBackend           string `json:"storageBackend"`
	MemoryAllocationInMb     int    `json:"memoryAllocationInMb"`
	BucketConflictResolution string `json:"bucketConflictResolution"`
	DurabilityLevel          string `json:"durabilityLevel"`
	Replicas                 int    `json:"replicas"`
	Flush                    bool   `json:"flush"`
	TimeToLiveInSeconds      int    `json:"timeToLiveInSeconds"`
	EvictionPolicy           string `json:"evictionPolicy"`
}

type listBucketsResponse struct {
	Data []*BucketInfo `json:"data"`
}

// The v4 API does not paginate this collection.
func (c *Client) ListBuckets(ctx context.Context, orgID, projectID, clusterID string) ([]*BucketInfo, error) {
	resp := &listBucketsResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets", orgID, projectID, clusterID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

type CreateBucketRequest struct {
	Name                     string `json:"name"`
	Type                     string `json:"type,omitempty"`
	StorageBackend           string `json:"storageBackend,omitempty"`
	MemoryAllocationInMb     int    `json:"memoryAllocationInMb,omitempty"`
	BucketConflictResolution string `json:"bucketConflictResolution,omitempty"`
	DurabilityLevel          string `json:"durabilityLevel,omitempty"`
	Replicas                 int    `json:"replicas,omitempty"`
	Flush                    bool   `json:"flush,omitempty"`
	TimeToLiveInSeconds      int    `json:"timeToLiveInSeconds,omitempty"`
}

type CreateBucketResponse struct {
	ID string `json:"id"`
}

func (c *Client) CreateBucket(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *CreateBucketRequest,
) (*CreateBucketResponse, error) {
	resp := &CreateBucketResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets", orgID, projectID, clusterID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) DeleteBucket(ctx context.Context, orgID, projectID, clusterID, bucketID string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets/%s",
		orgID, projectID, clusterID, bucketID)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}

type LoadSampleBucketRequest struct {
	Name string `json:"name"`
}

type LoadSampleBucketResponse struct {
	BucketID string `json:"bucketId"`
	Name     string `json:"name"`
}

// Only travel-sample, gamesim-sample and beer-sample are accepted.
func (c *Client) LoadSampleBucket(
	ctx context.Context,
	orgID, projectID, clusterID string,
	req *LoadSampleBucketRequest,
) (*LoadSampleBucketResponse, error) {
	resp := &LoadSampleBucketResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/sampleBuckets", orgID, projectID, clusterID)
	if err := c.doWrite(ctx, http.MethodPost, path, req, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

type ScopeInfo struct {
	Name        string           `json:"name"`
	Collections []CollectionInfo `json:"collections"`
}

type CollectionInfo struct {
	Name   string `json:"name"`
	MaxTTL int    `json:"maxTTL"`
}

type listScopesResponse struct {
	Scopes []ScopeInfo `json:"scopes"`
}

// bucketID is the base64 form of the bucket name.
func (c *Client) ListScopes(ctx context.Context, orgID, projectID, clusterID, bucketID string) ([]ScopeInfo, error) {
	resp := &listScopesResponse{}
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets/%s/scopes",
		orgID, projectID, clusterID, bucketID)
	if err := c.doRead(ctx, http.MethodGet, path, nil, resp); err != nil {
		return nil, err
	}
	return resp.Scopes, nil
}

type CreateScopeRequest struct {
	Name string `json:"name"`
}

func (c *Client) CreateScope(
	ctx context.Context,
	orgID, projectID, clusterID, bucketID string,
	req *CreateScopeRequest,
) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets/%s/scopes",
		orgID, projectID, clusterID, bucketID)
	return c.doWrite(ctx, http.MethodPost, path, req, nil)
}

func (c *Client) DeleteScope(ctx context.Context, orgID, projectID, clusterID, bucketID, scopeName string) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets/%s/scopes/%s",
		orgID, projectID, clusterID, bucketID, scopeName)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}

type CreateCollectionRequest struct {
	Name   string `json:"name"`
	MaxTTL int    `json:"maxTTL,omitempty"`
}

func (c *Client) CreateCollection(
	ctx context.Context,
	orgID, projectID, clusterID, bucketID, scopeName string,
	req *CreateCollectionRequest,
) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets/%s/scopes/%s/collections",
		orgID, projectID, clusterID, bucketID, scopeName)
	return c.doWrite(ctx, http.MethodPost, path, req, nil)
}

func (c *Client) DeleteCollection(
	ctx context.Context,
	orgID, projectID, clusterID, bucketID, scopeName, collectionName string,
) error {
	path := fmt.Sprintf("/v4/organizations/%s/projects/%s/clusters/%s/buckets/%s/scopes/%s/collections/%s",
		orgID, projectID, clusterID, bucketID, scopeName, collectionName)
	return c.doWrite(ctx, http.MethodDelete, path, nil, nil)
}
