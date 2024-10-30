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

type capellaError struct {
	ErrorName string `json:"error"`
	ErrorType string `json:"errorType"`
	Message   string `json:"message"`
	FullText  string
}

var _ error = capellaError{}

func (e capellaError) Error() string {
	return fmt.Sprintf("capella error Error:%s, ErrorType:%s Message:%s Full:%s",
		e.ErrorName, e.ErrorType, e.Message, e.FullText)
}

type requestError struct {
	StatusCode int
	Cause      error
}

var _ error = requestError{}

func (e requestError) Error() string {
	return fmt.Sprintf("request error (status: %d): %s", e.StatusCode, e.Cause)
}

func (e requestError) Unwrap() error {
	return e.Cause
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

		var parsedErr capellaError
		_ = json.Unmarshal(bytes, &parsedErr)
		parsedErr.FullText = string(bytes)

		return &requestError{
			StatusCode: resp.StatusCode,
			Cause:      &parsedErr,
		}
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
	for retryNum := 0; ; retryNum++ {
		req, err := makeReq()
		if err != nil {
			return errors.Wrap(err, "failed to build request")
		}

		err = c.doReq(ctx, req, out)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}

			// If the error contains 'Unauthorized' and we are using basic credentials
			// for JWT authentication, we refresh the token when this happens
			var capellaErr *capellaError
			if errors.As(err, &capellaErr) {
				if capellaErr.ErrorName == "Unauthorized" {
					basicAuth, _ := c.auth.(*BasicCredentials)
					if basicAuth != nil {
						c.logger.Debug("received unauthenticated error with basic credentials, refreshing jwt",
							zap.Error(err))

						reauthErr := c.updateJwtToken(ctx, basicAuth)
						if reauthErr != nil {
							return errors.Wrap(err,
								fmt.Sprintf("failed to update JWT token after failed request: %s", reauthErr))
						}

						continue
					}
				}
			}

			if retryNum == maxRetries {
				c.logger.Debug("request failed, exhausted retries",
					zap.Error(err),
					zap.Int("retryNum", retryNum),
					zap.Int("maxRetries", maxRetries))
				return err
			}

			retryTime := time.Duration(500+retryNum*100) * time.Millisecond
			c.logger.Debug("request failed, retrying",
				zap.Error(err),
				zap.Duration("retryTime", retryTime),
				zap.Int("retryNum", retryNum),
				zap.Int("maxRetries", maxRetries))
			time.Sleep(retryTime)
			continue
		}

		return nil
	}
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
				c.logger.Debug("refreshing jwt token")
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

func (c *Controller) doTokenRequest(
	ctx context.Context,
	method string,
	path string,
	token string,
	body interface{},
	out interface{},
) error {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return errors.Wrap(err, "failed to encode request body")
	}

	var bodyRdr io.Reader
	if body != nil {

		bodyRdr = bytes.NewReader(encodedBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bodyRdr)
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	if bodyRdr != nil {
		req.Header.Add("Content-Type", "application/json")
	}
	req.Header.Add("Authorization", "Bearer "+token)

	err = c.doReq(ctx, req, out)

	return err
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
	}, 3, &resp)
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

type ColumnarData struct {
	Config           ColumnarConfigInfo `json:"config"`
	CIDR             string             `json:"cidr"`
	CreatedByUser    string             `json:"createdByUser"`
	Description      string             `json:"description"`
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	TenantID         string             `json:"tenantId"`
	ProjectID        string             `json:"projectId"`
	ProjectName      string             `json:"projectName"`
	ScheduleCount    int                `json:"scheduleCount"`
	State            string             `json:"state"`
	Storage          ColumnarStorage    `json:"storage"`
	CreatedByUserID  string             `json:"createdByUserID"`
	UpsertedByUserID string             `json:"upsertedByUserID"`
	CreatedAt        string             `json:"createdAt"`
	UpsertedAt       string             `json:"upsertedAt"`
	ModifiedByUserID string             `json:"modifiedByUserID"`
	ModifiedAt       string             `json:"modifiedAt"`
	Version          int                `json:"version"`
}

type ColumnarConfigInfo struct {
	Provider         string `json:"provider"`
	Region           string `json:"region"`
	NodeCount        int    `json:"nodeCount"`
	HaveNodes        int    `json:"haveNodes"`
	WantNodes        int    `json:"wantNodes"`
	Endpoint         string `json:"endpoint"`
	Id               string `json:"clusterId"`
	AvailabilityZone string `json:"availabilityZone"`
	InstanceType     struct {
		VCPUs  string `json:"vcpus"`
		Memory string `json:"memory"`
	} `json:"instanceType"`
	Package struct {
		Type     string `json:"type"`
		Timezone string `json:"timezone"`
	} `json:"package"`
}

type ColumnarStorage struct {
	TotalBytes int `json:"totalBytes"`
}

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
	Config           ClusterInfo_Config  `json:"config"`
	Connect          ClusterInfo_Connect `json:"connect"`
	CreatedAt        time.Time           `json:"createdAt"`
	CreatedBy        string              `json:"createdBy"`
	CreatedByUserID  string              `json:"createdByUserID"`
	Description      string              `json:"description"`
	HasOnOffSchedule bool                `json:"hasOnOffSchedule"`
	Id               string              `json:"id"`
	ModifiedAt       time.Time           `json:"modifiedAt"`
	ModifiedBy       string              `json:"modifiedBy"`
	ModifiedByUserID string              `json:"modifiedByUserID"`
	Name             string              `json:"name"`
	// Package
	PlaygroundDisabled bool                  `json:"playgroundDisabled"`
	Project            ClusterInfo_Project   `json:"project"`
	Provider           ClusterInfo_Provider  `json:"provider"`
	Services           []ClusterInfo_Service `json:"services"`
	Status             ClusterInfo_Status    `json:"status"`
	TenantId           string                `json:"tenantId"`
	UpsertedAt         time.Time             `json:"upsertedAt"`
	UpsertedUserID     string                `json:"upsertedUserID"`
	Version            int                   `json:"version"`
}

type ClusterInfo_Config struct {
	Architecture  string `json:"architecture"`
	CustomImports bool   `json:"customImports"`
	SingleAz      bool   `json:"singleAz"`
	Version       string `json:"version"`
}

type ClusterInfo_Connect struct {
	Srv string `json:"srv"`
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

type ClusterInfo_Service struct {
	Compute         ClusterInfo_Service_Compute     `json:"compute"`
	Count           int                             `json:"count"`
	Disk            ClusterInfo_Service_Disk        `json:"disk"`
	DiskAutoScaling ClusterInfo_Service_DiskScaling `json:"diskAutoScaling"`
	Services        []ClusterInfo_Service_Service   `json:"services"`
}

type ClusterInfo_Service_Compute struct {
	Type       string `json:"type"`
	MemoryInGB int    `json:"memoryInGb"`
	Cpu        int    `json:"cpu"`
}

type ClusterInfo_Service_Disk struct {
	Type           string `json:"type"`
	SizeInGb       int    `json:"sizeInGb"`
	Iops           int    `json:"iops"`
	ThroughputMBPS int    `json:"throughputMbps"`
}

type ClusterInfo_Service_DiskScaling struct {
	Enabled bool `json:"enabled"`
}

type ClusterInfo_Service_Service struct {
	Type                 string `json:"type"`
	MemoryAllocationInMB int    `json:"memoryAllocationInMb"`
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

type ListColumnarsResponse PagedResourceResponse[*ColumnarData]

func (c *Controller) ListAllColumnars(
	ctx context.Context,
	tenantID string,
	req *PaginatedRequest,
) (*ListColumnarsResponse, error) {
	resp := &ListColumnarsResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/instance?%s", tenantID, form.Encode())
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

type DeployClusterRequest struct {
	CIDR        string                      `json:"cidr"`
	Description string                      `json:"description"`
	Name        string                      `json:"name"`
	Package     string                      `json:"package"`
	TenantId    string                      `json:"tenantId"`
	ProjectId   string                      `json:"projectId"`
	Provider    string                      `json:"provider"`
	Region      string                      `json:"region"`
	Server      string                      `json:"server"`
	Override    CreateOverrideRequest       `json:"overRide"`
	SingleAZ    bool                        `json:"singleAZ"`
	Specs       []DeployClusterRequest_Spec `json:"specs"`
	Timezone    string                      `json:"supportTimezone"`
}

type DeployClusterRequest_Spec struct {
	Compute         DeployClusterRequest_Spec_Compute     `json:"compute"`
	Count           int                                   `json:"count"`
	Disk            CreateClusterRequest_Spec_Disk        `json:"disk"`
	DiskAutoScaling CreateClusterRequest_Spec_DiskScaling `json:"diskAutoScaling"`
	Services        []CreateServices                      `json:"services"`
}

type DeployClusterRequest_Spec_Compute struct {
	Type   string `json:"type"`
	Cpu    int    `json:"cpu"`
	Memory int    `json:"memoryInGb"`
}

type CreateServices struct {
	Type string `json:"type"`
}

type CreateOverrideRequest struct {
	Image  string `json:"image"`
	Token  string `json:"token"`
	Server string `json:"server,omitempty"`
}

func (o CreateOverrideRequest) IsEmpty() bool {
	if o.Image == "" || o.Token == "" {
		return false
	}
	return true
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

type CreateColumnarInstanceRequest struct {
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Provider         string                `json:"provider"`
	Region           string                `json:"region"`
	Nodes            int                   `json:"nodes"`
	Package          Package               `json:"package"`
	InstanceTypes    ColumnarInstanceTypes `json:"instanceTypes"`
	AvailabilityZone string                `json:"availabilityZone"`
	Override         CreateOverrideRequest `json:"overRide,omitempty"`
}

type UpdateColumnarInstanceRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Nodes       int    `json:"nodes"`
}

type ColumnarInstanceTypes struct {
	VCPUs  string `json:"vcpus"`
	Memory string `json:"memory"`
}

type Package struct {
	Key      string `json:"key"`
	Timezone string `json:"timezone"`
}

func (c *Controller) CreateColumnar(
	ctx context.Context,
	tenantID string,
	projectID string,
	req *CreateColumnarInstanceRequest,
) (*CreateClusterResponse, error) {
	resp := &CreateClusterResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance", tenantID, projectID)
	err := c.doBasicReq(ctx, false, "POST", path, req, &resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
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

func (c *Controller) DeployCluster(
	ctx context.Context,
	tenantID string,
	req *DeployClusterRequest,
) (*CreateClusterResponse, error) {
	resp := &CreateClusterResponse{}
	path := fmt.Sprintf("/v2/organizations/%s/clusters/deploy", tenantID)
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

func (c *Controller) DeleteColumnar(
	ctx context.Context,
	tenantID, projectID string, clusterID string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s", tenantID, projectID, clusterID)
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

type UpdateClusterSpecsRequest_Spec struct {
	Compute         UpdateClusterSpecsRequest_Spec_Compute     `json:"compute"`
	Count           int                                        `json:"count"`
	Disk            UpdateClusterSpecsRequest_Spec_Disk        `json:"disk"`
	DiskAutoScaling UpdateClusterSpecsRequest_Spec_DiskScaling `json:"diskAutoScaling"`
	Services        []UpdateClusterSpecsRequest_Spec_Service   `json:"services"`
}

type UpdateClusterSpecsRequest_Spec_Compute struct {
	Type string `json:"type"`
}

type UpdateClusterSpecsRequest_Spec_Disk struct {
	Type     string `json:"type"`
	SizeInGb int    `json:"sizeInGb"`
	Iops     int    `json:"iops"`
}

type UpdateClusterSpecsRequest_Spec_DiskScaling struct {
	Enabled bool `json:"enabled"`
}

type UpdateClusterSpecsRequest_Spec_Service struct {
	Type string `json:"type"`
}

type UpdateClusterSpecsRequest struct {
	Specs []UpdateClusterSpecsRequest_Spec `json:"specs"`
}

func (c *Controller) UpdateClusterSpecs(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *UpdateClusterSpecsRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/specs", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) UpdateColumnarSpecs(
	ctx context.Context,
	tenantID string,
	projectID string,
	columnarID string,
	req *UpdateColumnarInstanceRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s", tenantID, projectID, columnarID)
	err := c.doBasicReq(ctx, false, "PATCH", path, req, nil)
	return err
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
	Provider       GetProviderDeploymentOptionsResponse_Provider       `json:"provider"`
	ServerVersions GetProviderDeploymentOptionsResponse_ServerVersions `json:"serverVersions"`
	SuggestedCidr  string                                              `json:"suggestedCidr"`
}

type GetProviderDeploymentOptionsResponse_Provider struct {
	AutoExpansion GetProviderDeploymentOptionsResponse_Provider_AutoExpansion `json:"autoExpansion"`
	DisplayName   string                                                      `json:"displayName"`
	// eligibility
	Key string `json:"key"`
	// regions
	// services
}

type GetProviderDeploymentOptionsResponse_Provider_AutoExpansion struct {
	Enabled bool `json:"enabled"`
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

func (c *Controller) ListAllowListEntriesColumnar(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PaginatedRequest,
) (*ListAllowListEntriesResponse, error) {
	resp := &ListAllowListEntriesResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/allowlists?%s", tenantID, projectID, clusterID, form.Encode())
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

func (c *Controller) AddAllowListEntryColumnar(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *UpdateAllowListEntriesRequest_Entry,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/allowlists", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) DeleteAllowListEntryColumnar(
	ctx context.Context,
	tenantID, projectID, clusterID, allowID string,
) error {

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/allowlists/%s", tenantID, projectID, clusterID, allowID)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
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

type UserInfo struct {
	ID          string                         `json:"ID"`
	Name        string                         `json:"name"`
	Permissions map[string]UserInfo_Permission `json:"permissions"`
}

type UserInfo_Permission struct {
	Buckets []string `json:"buckets"`
}

type ListUsersResponse PagedResourceResponse[*UserInfo]

func (c *Controller) ListUsers(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PaginatedRequest,
) (*ListUsersResponse, error) {
	resp := &ListUsersResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/users?%s",
		tenantID, projectID, clusterID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type CreateUserRequest struct {
	Name        string                                  `json:"name"`
	Password    string                                  `json:"password"`
	Permissions map[string]CreateUserRequest_Permission `json:"permissions"`
}

type CreateUserRequest_Permission struct {
	Buckets []string `json:"buckets,omitempty"`
}

func (c *Controller) CreateUser(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *CreateUserRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/users", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return err
}

func (c *Controller) DeleteUser(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	userId string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/users/%s",
		tenantID, projectID, clusterID,
		userId)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

func (c *Controller) CreateColumnarUser(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *CreateColumnarUserRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/apikeys", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return err
}

func (c *Controller) GetColumnarRoles(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PaginatedRequest,
) (*ListColumnarRolesResponse, error) {
	resp := &ListColumnarRolesResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/roles?%s",
		tenantID, projectID, clusterID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

func (c *Controller) ListColumnarUsers(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *PaginatedRequest,
) (*ListColumnarUsersResponse, error) {
	resp := &ListColumnarUsersResponse{}

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/apikeys?%s",
		tenantID, projectID, clusterID, form.Encode())
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

func (c *Controller) DeleteColumnarUser(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	userId string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/apikeys/%s",
		tenantID, projectID, clusterID,
		userId)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
	if err != nil {
		return err
	}

	return nil
}

type CreateColumnarUserRequest struct {
	Name       string   `json:"name"`
	Password   string   `json:"password"`
	Roles      []string `json:"roles"`
	Privileges struct {
		Privileges []string `json:"privileges"`
		Links      struct{} `json:"links"`
		Databases  struct{} `json:"databases"`
	} `json:"privileges"`
}

type ListColumnarUsersResponse PagedResourceResponse[*ColumnarGetUsersData]

type ColumnarGetUsersData struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Roles      []string `json:"roles"`
	Privileges struct{} `json:"privileges"`
	CreatedAt  string   `json:"createdAt"`
}

type ListColumnarRolesResponse PagedResourceResponse[*ColumnarGetRolesData]

type Privileges struct {
	Privileges []string `json:"privileges,omitempty"`
}

type ColumnarGetRolesData struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenantId"`
	ProjectID        string     `json:"projectId"`
	InstanceID       string     `json:"instanceId"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Privileges       Privileges `json:"privileges"`
	CreatedByUserID  string     `json:"createdByUserID"`
	UpsertedByUserID string     `json:"upsertedByUserID"`
	CreatedAt        string     `json:"createdAt"`
	UpsertedAt       string     `json:"upsertedAt"`
	ModifiedByUserID string     `json:"modifiedByUserID"`
	ModifiedAt       string     `json:"modifiedAt"`
	Version          int        `json:"version"`
}

type ListBucketsResponse struct {
	Buckets         Resource[[]Resource[ListBucketsResponse_Bucket]] `json:"buckets"`
	FreeMemoryInMb  int                                              `json:"freeMemoryInMb"`
	MaxReplicas     int                                              `json:"maxReplicas"`
	TotalMemoryInMb int                                              `json:"totalMemoryInMb"`
}

type ListBucketsResponse_Bucket struct {
	Name string `json:"name"`
	// ...
}

func (c *Controller) ListBuckets(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) (*ListBucketsResponse, error) {
	resp := &ListBucketsResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/buckets", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type CreateBucketRequest struct {
	// backupSchedule
	BucketConflictResolution string `json:"bucketConflictResolution"`
	DurabilityLevel          string `json:"durabilityLevel"`
	Flush                    bool   `json:"flush"`
	MemoryAllocationInMB     int    `json:"memoryAllocationInMb"`
	Name                     string `json:"name"`
	Replicas                 int    `json:"replicas"`
	StorageBackend           string `json:"storageBackend"`
	// timeToLive
	Type string `json:"type"`
}

func (c *Controller) CreateBucket(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *CreateBucketRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/buckets", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return err
}

func (c *Controller) DeleteBucket(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	bucketId string,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/buckets/%s",
		tenantID, projectID, clusterID,
		bucketId)
	err := c.doBasicReq(ctx, false, "DELETE", path, nil, nil)
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

func (c *Controller) GetTrustedCAs(
	ctx context.Context,
	clusterID string,
) (*GetTrustedCAsResponse, error) {
	resp := &GetTrustedCAsResponse{}

	path := fmt.Sprintf("/v2/databases/%s/proxy/pools/default/trustedCAs", clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

func (c *Controller) GetTrustedCAsColumnar(
	ctx context.Context,
	tenantID, projectID, clusterID string,
) (*GetTrustedCAsResponse, error) {
	resp := &GetTrustedCAsResponse{}

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/certificates", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}

	return resp, err
}

type UpdateServerVersionRequest struct {
	OverrideToken string `json:"token"`
	ServerImage   string `json:"image"`
	ServerVersion string `json:"server"`
	ReleaseId     string `json:"releaseId"`
}

func (c *Controller) UpdateServerVersion(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *UpdateServerVersionRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/version",
		tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	if err != nil {
		return err
	}

	return err
}

type StartCollectingServerLogsRequest struct {
	HostName string `json:"hostname"`
}

func (c *Controller) StartCollectingServerLogs(
	ctx context.Context,
	clusterID string,
	internalSupportToken string,
	req *StartCollectingServerLogsRequest,
) error {
	path := fmt.Sprintf("/internal/support/logcollections/clusters/%s", clusterID)
	err := c.doTokenRequest(ctx, "POST", path, internalSupportToken, req, nil)
	if err != nil {
		return err
	}

	return err
}

type DownloadServerLogsRequest struct {
	HostName string `json:"hostname"`
}

type DownloadServerLogsResponse struct {
	DownloadServerLogsStatuses []DownloadServerLogsStatus `json:"statuses,omitempty"`
}

type DownloadServerLogsStatus struct {
	StatusId                 string             `json:"statusId,omitempty"`
	Status                   string             `json:"status,omitempty"`
	Type                     string             `json:"type,omitempty"`
	Node                     string             `json:"node,omitempty"`
	PerNode                  map[string]PerNode `json:"perNode,omitempty"`
	Progress                 int                `json:"progress,omitempty"`
	Ts                       string             `json:"ts,omitempty"`
	RecommendedRefreshPeriod int                `json:"recommendedRefreshPeriod,omitempty"`
}

type PerNode struct {
	Path   string `json:"path,omitempty"`
	Status string `json:"status,omitempty"`
	Url    string `json:"url,omitempty"`
}

func (c *Controller) DownloadServerLogs(
	ctx context.Context,
	clusterID string,
	internalSupportToken string,
	req *DownloadServerLogsRequest,
) (*DownloadServerLogsResponse, error) {
	var resp []DownloadServerLogsStatus
	path := fmt.Sprintf("/internal/support/clusters/%s/pools/default/tasks", clusterID)
	err := c.doTokenRequest(ctx, "GET", path, internalSupportToken, req, &resp) // Pass pointer to slice

	if err != nil {
		return nil, err
	}

	return &DownloadServerLogsResponse{DownloadServerLogsStatuses: resp}, nil
}

func (c *Controller) RedeployCluster(
	ctx context.Context,
	clusterID string,
	internalSupportToken string,
) error {
	path := fmt.Sprintf("/internal/support/clusters/%s/deploy", clusterID)
	err := c.doTokenRequest(ctx, "POST", path, internalSupportToken, nil, nil)

	return err
}

type LoadColumnarSampleBucketRequest struct {
	SampleName string `url:"sampleName"`
}

func (c *Controller) LoadColumnarSampleBucket(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *LoadColumnarSampleBucketRequest,
) error {

	form, _ := query.Values(req)
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/proxy/api/v1/samples?%s", tenantID, projectID, clusterID, form.Encode())
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	return err
}

type LoadSampleBucketRequest struct {
	Name string `json:"name"`
}

func (c *Controller) LoadClusterSampleBucket(
	ctx context.Context,
	tenantID, projectID, clusterID string,
	req *LoadSampleBucketRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/clusters/%s/buckets/samples", tenantID, projectID, clusterID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	return err
}

type ProvisionedCluster struct {
	ClusterId string `json:"clusterId"`
}

type CreateColumnarCapellaLinkRequest struct {
	LinkName           string             `json:"linkName"`
	ProvisionedCluster ProvisionedCluster `json:"provisionedCluster"`
}

func (c *Controller) CreateColumnarCapellaLink(
	ctx context.Context,
	tenantID, projectID, columnarID string,
	req *CreateColumnarCapellaLinkRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/links", tenantID, projectID, columnarID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	return err
}

type CreateColumnarS3LinkRequest struct {
	Region          string `url:"region"`
	AccessKeyId     string `url:"accessKeyId"`
	SecretAccessKey string `url:"secretAccessKey"`
	SessionToken    string `url:"sessionToken"`
	Endpoint        string `url:"endpoint"`
	Type            string `url:"type"`
}

func (c *Controller) CreateColumnarS3Link(
	ctx context.Context,
	tenantID, projectID, columnarID, linkName string,
	req *CreateColumnarS3LinkRequest,
) error {
	form, _ := query.Values(req)

	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/proxy/analytics/link/%s?%s",
		tenantID, projectID, columnarID, linkName, form.Encode())
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	return err
}

type ColumnarQueryRequest struct {
	Statement   string `json:"statement"`
	MaxWarnings int    `json:"max-warnings"`
}

// Expect no results
func (c *Controller) DoBasicColumnarQuery(
	ctx context.Context,
	tenantID, projectID, columnarID string,
	req *ColumnarQueryRequest,
) error {
	path := fmt.Sprintf("/v2/organizations/%s/projects/%s/instance/%s/proxy/analytics/service", tenantID, projectID, columnarID)
	err := c.doBasicReq(ctx, false, "POST", path, req, nil)
	return err
}
