package capellacontrol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-querystring/query"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Credentials interface {
	isCredentials() bool
}

type BasicCredentials struct {
	Username string
	Password string

	jwtToken string
}

var _ Credentials = (*BasicCredentials)(nil)

func (c BasicCredentials) isCredentials() bool { return true }

type TokenCredentials struct {
	AccessKey string
	SecretKey string
}

var _ Credentials = (*TokenCredentials)(nil)

func (c TokenCredentials) isCredentials() bool { return true }

type Controller struct {
	logger     *zap.Logger
	httpClient *http.Client
	endpoint   string
	auth       Credentials
}

type ControllerOptions struct {
	Logger     *zap.Logger
	HttpClient *http.Client
	Endpoint   string
	Auth       Credentials
}

func NewController(ctx context.Context, opts *ControllerOptions) (*Controller, error) {
	if opts == nil {
		opts = &ControllerOptions{}
	}

	httpClient := opts.HttpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	switch opts.Auth.(type) {
	case *BasicCredentials:
	case *TokenCredentials:
	default:
		return nil, errors.New("invalid auth type")
	}

	return &Controller{
		logger:     opts.Logger,
		httpClient: httpClient,
		endpoint:   opts.Endpoint,
		auth:       opts.Auth,
	}, nil
}

func (c *Controller) doReq(
	ctx context.Context,
	req *http.Request,
	out interface{},
) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to execute auth request")
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
	req, err := makeReq()
	if err != nil {
		return errors.Wrap(err, "failed to build request")
	}

	return c.doReq(ctx, req, out)
}

func (c *Controller) doBasicReq(
	ctx context.Context,
	allowRetries bool,
	method string,
	path string,
	body interface{},
	out interface{},
) error {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return errors.Wrap(err, "failed to encode request body")
	}

	maxRetries := 10
	if !allowRetries {
		maxRetries = 0
	}

	return c.doRetriableReq(ctx, func() (*http.Request, error) {
		var bodyRdr io.Reader
		if body != nil {

			bodyRdr = bytes.NewReader(encodedBody)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bodyRdr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create request")
		}

		if bodyRdr != nil {
			req.Header.Add("Content-Type", "application/json")
		}

		switch auth := c.auth.(type) {
		case *BasicCredentials:
			if auth.jwtToken == "" {
				err = c.updateJwtToken(ctx, auth)
				if err != nil {
					return nil, errors.Wrap(err, "failed to update jwt token")
				}
			}

			req.Header.Add("Authorization", "Bearer "+auth.jwtToken)
		case *TokenCredentials:
			// NOTE: This does not appear to actually work right now?

			reqTimeStr := strconv.FormatInt(time.Now().Unix(), 10)

			payload := strings.Join([]string{method, path, reqTimeStr}, "\n")
			reqHash := hmac.New(sha256.New, []byte(auth.SecretKey))
			reqHash.Write([]byte(payload))
			reqHashStr := base64.StdEncoding.EncodeToString(reqHash.Sum(nil))

			req.Header.Add("Couchbase-Timestamp", reqTimeStr)
			req.Header.Add("Authorization", "Bearer "+auth.AccessKey+":"+reqHashStr)
		default:
			return nil, errors.New("invalid auth type")
		}

		return req, nil
	}, maxRetries, out)
}

func (c *Controller) updateJwtToken(ctx context.Context, auth *BasicCredentials) error {
	var resp struct {
		Jwt string `json:"jwt"`
	}

	err := c.doRetriableReq(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/sessions", nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create request")
		}

		req.SetBasicAuth(auth.Username, auth.Password)
		return req, nil
	}, 10, &resp)
	if err != nil {
		return err
	}

	auth.jwtToken = resp.Jwt
	return nil
}

type PaginatedRequest struct {
	Page          int    `url:"page"`
	PerPage       int    `url:"perPage"`
	SortBy        string `url:"sortBy"`
	SortDirection string `url:"sortDirection"`
}

type ResourcePermissionEntry struct {
	Accessible bool
}

type ResourcePermissions struct {
	Create ResourcePermissionEntry
	Delete ResourcePermissionEntry
	Read   ResourcePermissionEntry
	Update ResourcePermissionEntry
}

type ResponseCursorPages struct {
	Last       int `json:"last"`
	Page       int `json:"page"`
	PerPage    int `json:"perPage"`
	TotalItems int `json:"totalItems"`
}

type ResponseCursor struct {
	//Hrefs map[string]?? `json:"hrefs"`
	Pages *ResponseCursorPages `json:"pages"`
}

type Resource[T any] struct {
	Data        T                               `json:"data"`
	Permissions *ResourcePermissions            `json:"permissions"`
	Resources   map[string]*ResourcePermissions `json:"resources"`
}

type PagedResponse[T any] struct {
	Cursor *ResponseCursor `json:"cursor"`
	Resource[[]T]
}

type PagedResourceResponse[T any] PagedResponse[Resource[T]]

type ResourceResponse[T any] Resource[T]

type ProjectInfo struct {
	ClusterCount       int       `json:"clusterCount"`
	CreatedAt          time.Time `json:"createdAt"`
	CreatedByUserID    string    `json:"createdByUserID"`
	CreatedByUsername  string    `json:"createdByUsername"`
	Description        string    `json:"description"`
	ID                 string    `json:"id"`
	ModifiedAt         time.Time `json:"modifiedAt"`
	ModifiedByUserID   string    `json:"modifiedByUserID"`
	ModifiedByUsername string    `json:"modifiedByUsername"`
	Name               string    `json:"name"`
	SyncGWCount        int       `json:"syncGWCount"`
	TenantID           string    `json:"tenantId"`
	UpsertedAt         time.Time `json:"upsertedAt"`
	UpsertedByUserID   string    `json:"upsertedByUserID"`
	UserCount          int       `json:"userCount"`
	Version            int       `json:"version"`
}

type ListProjectsResponse PagedResourceResponse[*ProjectInfo]

func (c *Controller) ListProjects(
	ctx context.Context,
	tenantID string,
	req *PaginatedRequest,
) (*ListProjectsResponse, error) {
	resp := &ListProjectsResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects?%s", tenantID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type CreateProjectRequest struct {
	Name string `json:"name"`
}

type CreateProjectResponse struct {
	Id string `json:"id"`
}

func (c *Controller) CreateProject(
	ctx context.Context,
	tenantID string,
	req *CreateProjectRequest,
) (*CreateProjectResponse, error) {
	resp := &CreateProjectResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects", tenantID)
	err := c.doBasicReq(ctx, false, "POST", path, req, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type UpdateProjectRequest struct {
	Name string `json:"name"`
}

type UpdateProjectResponse PagedResourceResponse[*ProjectInfo]

func (c *Controller) UpdateProject(
	ctx context.Context,
	tenantID, projectID string,
	req *UpdateProjectRequest,
) (*UpdateProjectResponse, error) {
	resp := &UpdateProjectResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s", tenantID, projectID)
	err := c.doBasicReq(ctx, false, "PUT", path, req, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Controller) DeleteProject(
	ctx context.Context,
	tenantID, projectID string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s", tenantID, projectID)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

type ClusterInfo struct {
	Config ClusterInfo_Config `json:"config"`
	// Connect
	CreatedAt        time.Time `json:"createdAt"`
	CreatedBy        string    `json:"createdBy"`
	CreatedByUserID  string    `json:"createdByUserID"`
	Description      string    `json:"description"`
	HasOnOffSchedule bool      `json:"hasOnOffSchedule"`
	Id               string    `json:"id"`
	ModifiedAt       time.Time `json:"modifiedAt"`
	ModifiedBy       string    `json:"modifiedBy"`
	ModifiedByUserID string    `json:"modifiedByUserID"`
	Name             string    `json:"name"`
	// Package
	PlaygroundDisabled bool                 `json:"playgroundDisabled"`
	Project            ClusterInfo_Project  `json:"project"`
	Provider           ClusterInfo_Provider `json:"provider"`
	// Services
	Status         ClusterInfo_Status `json:"status"`
	TenantId       string             `json:"tenantId"`
	UpsertedAt     time.Time          `json:"upsertedAt"`
	UpsertedUserID string             `json:"upsertedUserID"`
	Version        int                `json:"version"`
}

type ClusterInfo_Config struct {
	Architecture  string `json:"architecture"`
	CustomImports bool   `json:"customImports"`
	SingleAz      bool   `json:"singleAz"`
	Version       string `json:"version"`
}

type ClusterInfo_Project struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

type ClusterInfo_Provider struct {
	DeliveryMethod string `json:"deliveryMethod"`
	Name           string `json:"name"`
	Region         string `json:"region"`
}

type ClusterInfo_Status struct {
	State string `json:"state"`
}

type ListClustersResponse PagedResourceResponse[*ClusterInfo]

func (c *Controller) ListAllClusters(
	ctx context.Context,
	tenantID string,
	req *PaginatedRequest,
) (*ListClustersResponse, error) {
	resp := &ListClustersResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/clusters?%s", tenantID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type CreateClusterRequest struct {
	CIDR        string                      `json:"cidr"`
	Description string                      `json:"description"`
	Name        string                      `json:"name"`
	Plan        string                      `json:"plan"`
	ProjectId   string                      `json:"projectId"`
	Provider    string                      `json:"provider"`
	Region      string                      `json:"region"`
	Server      string                      `json:"server"`
	SingleAZ    bool                        `json:"singleAZ"`
	Specs       []CreateClusterRequest_Spec `json:"specs"`
	Timezone    string                      `json:"timezone"`
}

type CreateClusterRequest_Spec struct {
	Compute         string                                `json:"compute"`
	Count           int                                   `json:"count"`
	Disk            CreateClusterRequest_Spec_Disk        `json:"disk"`
	DiskAutoScaling CreateClusterRequest_Spec_DiskScaling `json:"diskAutoScaling"`
	Provider        string                                `json:"provider"`
	Services        []string                              `json:"services"`
}

type CreateClusterRequest_Spec_Disk struct {
	Type     string `json:"type"`
	SizeInGb int    `json:"sizeInGb"`
	Iops     int    `json:"iops"`
}

type CreateClusterRequest_Spec_DiskScaling struct {
	Enabled bool `json:"enabled"`
}

type CreateClusterResponse struct {
	Id string `json:"id"`
}

func (c *Controller) CreateCluster(
	ctx context.Context,
	tenantID string,
	req *CreateClusterRequest,
) (*CreateClusterResponse, error) {
	resp := &CreateClusterResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/clusters", tenantID)
	err := c.doBasicReq(ctx, false, "POST", path, req, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (c *Controller) DeleteCluster(
	ctx context.Context,
	tenantID, projectID string, clusterID string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

type UpdateClusterMetaRequest struct {
	Description string `json:"description"`
	Name        string `json:"name"`
}

func (c *Controller) UpdateClusterMeta(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *UpdateClusterMetaRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/meta", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return nil
}

type ClusterJobInfo struct {
	JobType              string    `json:"jobType"`
	ID                   string    `json:"id"`
	ClusterID            string    `json:"clusterId"`
	ClusterName          string    `json:"clusterName"`
	ProjectID            string    `json:"projectId"`
	TenantID             string    `json:"tenantId"`
	StartTime            time.Time `json:"startTime"`
	CompletionPercentage int       `json:"completionPercentage"`
	CurrentStep          string    `json:"currentStep"`
	InitiatedBy          string    `json:"initiatedBy"`
	JobResourceType      string    `json:"jobResourceType"`
}

type ListClusterJobsResponse PagedResourceResponse[*ClusterJobInfo]

func (c *Controller) ListClusterJobs(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) (*ListClusterJobsResponse, error) {
	resp := &ListClusterJobsResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/jobs", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type GetProviderDeploymentOptionsRequest struct {
	Provider string `url:"provider"`
}

type GetProviderDeploymentOptionsResponse struct {
	CidrBlacklist []string `json:"cidrBlacklist"`
	// deliveryMethods
	// plans
	// projects
	ServerVersions GetProviderDeploymentOptionsResponse_ServerVersions `json:"serverVersions"`
	SuggestedCidr  string                                              `json:"suggestedCidr"`
}

type GetProviderDeploymentOptionsResponse_ServerVersions struct {
	DefaultVersion string   `json:"defaultVersion"`
	Versions       []string `json:"versions"`
}

func (c *Controller) GetProviderDeploymentOptions(
	ctx context.Context,
	tenantID string,
	req *GetProviderDeploymentOptionsRequest,
) (*GetProviderDeploymentOptionsResponse, error) {
	resp := &GetProviderDeploymentOptionsResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/clusters/deployment-options?%s", tenantID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type AllowListEntryInfo struct {
	ID        string    `json:"id"`
	Cidr      string    `json:"cidr"`
	Comment   string    `json:"comment"`
	CreatedAt time.Time `json:"createdAt"`
	Type      string    `json:"type"`   // permanent
	Status    string    `json:"status"` // active
	CreatedBy string    `json:"createdBy"`
}

type ListAllowListEntriesResponse PagedResourceResponse[*AllowListEntryInfo]

func (c *Controller) ListAllowListEntries(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PaginatedRequest,
) (*ListAllowListEntriesResponse, error) {
	resp := &ListAllowListEntriesResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/allowlists?%s", tenantID, projectID, clusterID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

type UpdateAllowListEntriesRequest struct {
	Create []UpdateAllowListEntriesRequest_Entry `json:"create"`
	Delete []string                              `json:"delete"`
}

type UpdateAllowListEntriesRequest_Entry struct {
	Cidr      string `json:"cidr"`
	Comment   string `json:"comment"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

func (c *Controller) UpdateAllowListEntries(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *UpdateAllowListEntriesRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/allowlists-bulk", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) EnablePrivateEndpoints(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) DisablePrivateEndpoints(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

type PrivateEndpointInfo struct {
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"` // idle, enabling, enabled
}

type GetPrivateEndpointResponse ResourceResponse[PrivateEndpointInfo]

func (c *Controller) GetPrivateEndpoint(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) (*GetPrivateEndpointResponse, error) {
	resp := &GetPrivateEndpointResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type PrivateEndpointDetailsInfo struct {
	Enabled     bool   `json:"enabled"`
	PrivateDNS  string `json:"privateDns"`
	ServiceName string `json:"serviceName"`
}

func (c *Controller) GetPrivateEndpointDetails(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) (*ResourceResponse[PrivateEndpointDetailsInfo], error) {
	resp := &ResourceResponse[PrivateEndpointDetailsInfo]{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint/details", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type PrivateEndpointLinkInfo struct {
	EndpointID string    `json:"endpointId"`
	Status     string    `json:"status"` // pendingAcceptance, pending, linked, rejected
	CreatedAt  time.Time `json:"createdAt"`
}

type ListPrivateEndpointLinksResponse PagedResponse[*PrivateEndpointLinkInfo]

func (c *Controller) ListPrivateEndpointLinks(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) (*ListPrivateEndpointLinksResponse, error) {
	resp := &ListPrivateEndpointLinksResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint/connection", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type PrivateEndpointLinkRequest struct {
	VpcID     string `json:"vpcId"`
	SubnetIds string `json:"subnetIds"` // this is a space-delimited list of subnet-ids
}

type PrivateEndpointLinkSetupInfo struct {
	Command string `json:"command"`
}

type CreatePrivateEndpointLinkResponse ResourceResponse[PrivateEndpointLinkSetupInfo]

// This isn't actually neccessary, it's used by the UI to generate the aws link
// command to use to link to the VPC.
/*
   Example Output:
     aws ec2 create-vpc-endpoint
       --vpc-id vpc-0ea6734517a89f0f9
	   --region us-west-2
	   --service-name com.amazonaws.vpce.us-west-2.vpce-svc-048c94c79e2d1249a
	   --vpc-endpoint-type Interface
	   --subnet-ids subnet-03b3b018d16b1e599 subnet-066bf3b21c106d96b
*/
func (c *Controller) GenPrivateEndpointLinkCommand(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PrivateEndpointLinkRequest,
) (*CreatePrivateEndpointLinkResponse, error) {
	resp := &CreatePrivateEndpointLinkResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint/linkcommand", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type PrivateEndpointAcceptLinkRequest struct {
	EndpointID string `json:"endpointId"`
}

func (c *Controller) AcceptPrivateEndpointLink(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PrivateEndpointAcceptLinkRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/privateendpoint/connection", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return err
}
