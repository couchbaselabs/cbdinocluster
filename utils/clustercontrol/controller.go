package clustercontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-querystring/query"
	"github.com/pkg/errors"
)

type Controller struct {
	Endpoint string
}

func (c *Controller) doReq(ctx context.Context, req *http.Request, out interface{}) error {
	client := &http.Client{}

	req.SetBasicAuth("Administrator", "password")

	resp, err := client.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to execute request")
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("non-200 status code encountered: %d %s", resp.StatusCode, bytes)
	}

	if out != nil {
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(out)
		if err != nil {
			return errors.Wrap(err, "failed to decode response")
		}
	}

	return nil
}

func (c *Controller) doRetriableReq(ctx context.Context, makeReq func() (*http.Request, error), maxRetries int, out interface{}) error {
	retryNum := 0
	for {
		req, err := makeReq()
		if err != nil {
			return errors.Wrap(err, "failed to build request")
		}

		err = c.doReq(ctx, req, out)
		if err != nil {
			if maxRetries == 0 {
				return err
			}

			if retryNum >= maxRetries {
				// after 10 retries we just return the error
				return errors.Wrap(err, fmt.Sprintf("failed after %d retries", maxRetries))
			}

			retryNum++

			select {
			case <-time.After(1 * time.Second):
				// continue
			case <-ctx.Done():
				return ctx.Err()
			}

			continue
		}

		return nil
	}
}

func (c *Controller) doGet(ctx context.Context, path string, out interface{}) error {
	maxRetries := 10
	return c.doRetriableReq(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+path, nil)
	}, maxRetries, out)
}

func (c *Controller) doDelete(ctx context.Context, path string, out interface{}) error {
	maxRetries := 10
	return c.doRetriableReq(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodDelete, c.Endpoint+path, nil)
	}, maxRetries, out)
}

func (c *Controller) doFormReq(ctx context.Context, method string, path string, data url.Values, allowRetries bool, out interface{}) error {
	encodedData := data.Encode()

	maxRetries := 10
	if !allowRetries {
		maxRetries = 0
	}

	return c.doRetriableReq(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, strings.NewReader(encodedData))
		if err != nil {
			return nil, err
		}

		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		return req, nil
	}, maxRetries, out)
}

func (c *Controller) doFormPost(ctx context.Context, path string, data url.Values, allowRetries bool, out interface{}) error {
	return c.doFormReq(ctx, http.MethodPost, path, data, allowRetries, out)
}

func (c *Controller) doFormPut(ctx context.Context, path string, data url.Values, allowRetries bool, out interface{}) error {
	return c.doFormReq(ctx, http.MethodPut, path, data, allowRetries, out)
}

func (c *Controller) Ping(ctx context.Context) error {
	return c.doRetriableReq(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+"/pools", nil)
	}, 0, nil)
}

type NodeInitOptions struct {
	Hostname string
	Afamily  string
}

func (c *Controller) NodeInit(ctx context.Context, opts *NodeInitOptions) error {
	form := make(url.Values)
	if opts.Hostname != "" {
		form.Add("hostname", opts.Hostname)
	}
	if opts.Afamily != "" {
		form.Add("afamily", opts.Afamily)
	}
	return c.doFormPost(ctx, "/nodeInit", form, true, nil)
}

type UpdateDefaultPoolOptions struct {
	ClusterName           string
	KvMemoryQuotaMB       int
	IndexMemoryQuotaMB    int
	FtsMemoryQuotaMB      int
	CbasMemoryQuotaMB     int
	EventingMemoryQuotaMB int
}

func (c *Controller) UpdateDefaultPool(ctx context.Context, opts *UpdateDefaultPoolOptions) error {
	form := make(url.Values)
	if opts.ClusterName != "" {
		form.Add("clusterName", opts.ClusterName)
	}
	if opts.KvMemoryQuotaMB > 0 {
		form.Add("memoryQuota", fmt.Sprintf("%d", opts.KvMemoryQuotaMB))
	}
	if opts.IndexMemoryQuotaMB > 0 {
		form.Add("indexMemoryQuota", fmt.Sprintf("%d", opts.IndexMemoryQuotaMB))
	}
	if opts.FtsMemoryQuotaMB > 0 {
		form.Add("ftsMemoryQuota", fmt.Sprintf("%d", opts.FtsMemoryQuotaMB))
	}
	if opts.CbasMemoryQuotaMB > 0 {
		form.Add("cbasMemoryQuota", fmt.Sprintf("%d", opts.CbasMemoryQuotaMB))
	}
	if opts.EventingMemoryQuotaMB > 0 {
		form.Add("eventingMemoryQuota", fmt.Sprintf("%d", opts.EventingMemoryQuotaMB))
	}
	return c.doFormPost(ctx, "/pools/default", form, true, nil)
}

type EnableExternalListenerOptions struct {
	Afamily        string
	NodeEncryption string
}

func (c *Controller) EnableExternalListener(ctx context.Context, opts *EnableExternalListenerOptions) error {
	form := make(url.Values)
	if opts.Afamily != "" {
		form.Add("afamily", opts.Afamily)
	}
	if opts.NodeEncryption != "" {
		form.Add("nodeEncryption", opts.NodeEncryption)
	}
	return c.doFormPost(ctx, "/node/controller/enableExternalListener", form, true, nil)
}

type SetupNetConfigOptions struct {
	Afamily        string
	NodeEncryption string
}

func (c *Controller) SetupNetConfig(ctx context.Context, opts *SetupNetConfigOptions) error {
	form := make(url.Values)
	if opts.Afamily != "" {
		form.Add("afamily", opts.Afamily)
	}
	if opts.NodeEncryption != "" {
		form.Add("nodeEncryption", opts.NodeEncryption)
	}
	return c.doFormPost(ctx, "/node/controller/setupNetConfig", form, true, nil)
}

func (c *Controller) DisableUnusedExternalListeners(ctx context.Context) error {
	return c.doFormPost(ctx, "/node/controller/disableUnusedExternalListeners", url.Values{}, true, nil)
}

type UpdateIndexSettingsOptions struct {
	StorageMode string
}

func (c *Controller) UpdateIndexSettings(ctx context.Context, opts *UpdateIndexSettingsOptions) error {
	form := make(url.Values)
	if opts.StorageMode != "" {
		form.Add("storageMode", opts.StorageMode)
	}
	return c.doFormPost(ctx, "/settings/indexes", form, true, nil)
}

type UpdateWebSettingsOptions struct {
	Username string
	Password string
}

func (c *Controller) UpdateWebSettings(ctx context.Context, opts *UpdateWebSettingsOptions) error {
	form := make(url.Values)
	if opts.Username != "" {
		form.Add("username", opts.Username)
	}
	if opts.Password != "" {
		form.Add("password", opts.Password)
	}
	form.Add("port", "SAME")
	return c.doFormPost(ctx, "/settings/web", form, true, nil)
}

type SetupServicesOptions struct {
	Services []string
}

func (c *Controller) SetupServices(ctx context.Context, opts *SetupServicesOptions) error {
	form := make(url.Values)
	if len(opts.Services) > 0 {
		form.Add("services", strings.Join(opts.Services, ","))
	}
	return c.doFormPost(ctx, "/node/controller/setupServices", form, true, nil)
}

type AddNodeOptions struct {
	ServerGroup string

	Address  string
	Services []string
	Username string
	Password string
}

func (c *Controller) AddNode(ctx context.Context, opts *AddNodeOptions) error {
	form := make(url.Values)
	form.Add("hostname", opts.Address)
	form.Add("services", strings.Join(opts.Services, ","))
	form.Add("user", opts.Username)
	form.Add("password", opts.Password)

	path := fmt.Sprintf("/pools/default/serverGroups/%s/addNode", opts.ServerGroup)
	return c.doFormPost(ctx, path, form, true, nil)
}

type LocalInfo struct {
	OTPNode  string
	Services []string
}

func (c *Controller) GetLocalInfo(ctx context.Context) (*LocalInfo, error) {
	var resp struct {
		Nodes []struct {
			ThisNode bool     `json:"thisNode"`
			OTPNode  string   `json:"otpNode"`
			Services []string `json:"services"`
		} `json:"nodes"`
	}
	err := c.doGet(ctx, "/pools/default", &resp)
	if err != nil {
		return nil, err
	}

	for _, node := range resp.Nodes {
		if node.ThisNode {
			return &LocalInfo{
				OTPNode:  node.OTPNode,
				Services: node.Services,
			}, nil
		}
	}

	return nil, errors.New("no node was marked as this node")
}

func (c *Controller) ListNodeOTPs(ctx context.Context) ([]string, error) {
	var resp struct {
		Nodes []struct {
			OTPNode string `json:"otpNode"`
		} `json:"nodes"`
	}
	err := c.doGet(ctx, "/pools/default", &resp)
	if err != nil {
		return nil, err
	}

	nodeOtps := make([]string, len(resp.Nodes))
	for nodeIdx, node := range resp.Nodes {
		nodeOtps[nodeIdx] = node.OTPNode
	}

	return nodeOtps, nil
}

type BeginRebalanceOptions struct {
	KnownNodeOTPs   []string
	EjectedNodeOTPs []string
}

func (c *Controller) BeginRebalance(ctx context.Context, opts *BeginRebalanceOptions) error {
	form := make(url.Values)
	form.Add("knownNodes", strings.Join(opts.KnownNodeOTPs, ","))
	form.Add("ejectedNodes", strings.Join(opts.EjectedNodeOTPs, ","))

	return c.doFormPost(ctx, "/controller/rebalance", form, true, nil)
}

type Task struct {
	Status string
}

func (c *Controller) ListTasks(ctx context.Context) ([]*Task, error) {
	var resp []struct {
		Status string `json:"status"`
	}
	err := c.doGet(ctx, "/pools/default/tasks", &resp)
	if err != nil {
		return nil, err
	}

	tasks := make([]*Task, len(resp))
	for statusIdx, status := range resp {
		tasks[statusIdx] = &Task{
			Status: status.Status,
		}
	}

	return tasks, nil
}

type ListUsersRequest struct {
	Order    string `url:"order"`
	PageSize int    `url:"pageSize"`
	SortBy   string `url:"sortBy"`
}

type ListUsersResponse struct {
	Total int `json:"total"`
	// links
	// skipped
	Users []ListUsersResponse_User `json:"users"`
}

type ListUsersResponse_User struct {
	ID     string                        `json:"id"`
	Domain string                        `json:"domain"`
	Roles  []ListUsersResponse_User_Role `json:"roles"`
	// groups
	// external_groups
	Name               string `json:"name"`
	Uuid               string `json:"uuid"`
	PasswordChangeDate string `json:"password_change_date"`
}

type ListUsersResponse_User_Role struct {
	Role           string                               `json:"role"`
	BucketName     string                               `json:"bucket_name"`
	ScopeName      string                               `json:"scope_name"`
	CollectionName string                               `json:"collection_name"`
	Origins        []ListUsersResponse_User_Role_Origin `json:"origins"`
}

type ListUsersResponse_User_Role_Origin struct {
	Type string `json:"type"`
}

func (c *Controller) ListUsers(ctx context.Context, req *ListUsersRequest) (*ListUsersResponse, error) {
	resp := &ListUsersResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/settings/rbac/users?%s", form.Encode())
	err := c.doGet(ctx, path, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type CreateUserRequest struct {
	Roles    []string `url:"roles,comma"`
	Name     string   `url:"name"`
	Groups   []string `url:"groups,comma"`
	Password string   `url:"password"`
}

func (c *Controller) CreateUser(ctx context.Context, username string, req *CreateUserRequest) error {
	form, _ := query.Values(req)
	path := fmt.Sprintf("/settings/rbac/users/local/%s", username)
	err := c.doFormPut(ctx, path, form, true, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) DeleteUser(ctx context.Context, username string) error {
	path := fmt.Sprintf("/settings/rbac/users/local/%s", username)
	err := c.doDelete(ctx, path, nil)
	if err != nil {
		return err
	}

	return nil
}

type ListBucketsResponse_Bucket struct {
	Name string `json:"name"`
}

func (c *Controller) ListBuckets(ctx context.Context) ([]ListBucketsResponse_Bucket, error) {
	var resp []ListBucketsResponse_Bucket

	path := "/pools/default/buckets"
	err := c.doGet(ctx, path, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type CreateBucketRequest struct {
	Name                   string `url:"name"`
	BucketType             string `url:"bucketType"`
	StorageBackend         string `url:"storageBackend"`
	AutoCompactionDefined  bool   `url:"autoCompactionDefined"`
	EvictionPolicy         string `url:"evictionPolicy"`
	ThreadsNumber          int    `url:"threadsNumber"`
	ReplicaNumber          int    `url:"replicaNumber"`
	DurabilityMinLevel     string `url:"durabilityMinLevel"`
	CompressionMode        string `url:"compressionMode"`
	MaxTTL                 int    `url:"maxTTL"`
	ReplicaIndex           int    `url:"replicaIndex"`
	ConflictResolutionType string `url:"conflictResolutionType"`
	RamQuotaMB             int    `url:"ramQuotaMB"`
	FlushEnabled           bool   `url:"flushEnabled,int"`
}

func (c *Controller) CreateBucket(ctx context.Context, req *CreateBucketRequest) error {
	form, _ := query.Values(req)
	path := "/pools/default/buckets"
	err := c.doFormPost(ctx, path, form, true, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) DeleteBucket(ctx context.Context, bucketName string) error {
	path := fmt.Sprintf("/pools/default/buckets/%s", bucketName)
	err := c.doDelete(ctx, path, nil)
	if err != nil {
		return err
	}

	return nil
}

type GetTrustedCAsResponse []GetTrustedCAsResponse_Certificate

type GetTrustedCAsResponse_Certificate struct {
	ID        int    `json:"id"`
	Subject   string `json:"subject"`
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter"`
	Pem       string `json:"pem"`
}

func (c *Controller) GetTrustedCAs(ctx context.Context) (*GetTrustedCAsResponse, error) {
	resp := &GetTrustedCAsResponse{}

	path := "/pools/default/trustedCAs"
	err := c.doGet(ctx, path, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
