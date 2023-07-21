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

	if resp.StatusCode != 200 {
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

func (c *Controller) doFormPost(ctx context.Context, path string, data url.Values, allowRetries bool, out interface{}) error {
	encodedData := data.Encode()

	maxRetries := 10
	if !allowRetries {
		maxRetries = 0
	}

	return c.doRetriableReq(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+path, strings.NewReader(encodedData))
		if err != nil {
			return nil, err
		}

		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		return req, nil
	}, maxRetries, out)
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
	KnownNodeOTPs []string
}

func (c *Controller) BeginRebalance(ctx context.Context, opts *BeginRebalanceOptions) error {
	form := make(url.Values)
	form.Add("knownNodes", strings.Join(opts.KnownNodeOTPs, ","))

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
