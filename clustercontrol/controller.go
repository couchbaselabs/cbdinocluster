package clustercontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pkg/errors"
)

type Controller struct {
	Endpoint string
}

type NodeSetupOptions struct {
	EnableKvService       bool
	EnableN1qlService     bool
	EnableIndexService    bool
	EnableFtsService      bool
	EnableCbasService     bool
	EnableEventingService bool
	EnableBackupService   bool
}

type SetupFirstNodeOptions struct {
	KvMemoryQuotaMB       int
	IndexMemoryQuotaMB    int
	FtsMemoryQuotaMB      int
	CbasMemoryQuotaMB     int
	EventingMemoryQuotaMB int

	Username string
	Password string

	NodeSetupOptions
}

type AddNodeOptions struct {
	Address string

	NodeSetupOptions
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

		return fmt.Errorf("non-200 status code encountered: %s", bytes)
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

func (c *Controller) doGet(ctx context.Context, path string, out interface{}) error {
	for {
		req, err := http.NewRequest(http.MethodGet, c.Endpoint+path, nil)
		if err != nil {
			return errors.Wrap(err, "failed to build request")
		}

		err = c.doReq(ctx, req, out)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// TODO(brett19): need to deal with context deadline here
				time.Sleep(1 * time.Second)
				continue
			}

			return err
		}

		return nil
	}
}

func (c *Controller) doFormPost(ctx context.Context, path string, data url.Values, allowRetries bool, out interface{}) error {
	encodedData := data.Encode()

	for {
		req, err := http.NewRequest(http.MethodPost, c.Endpoint+path, strings.NewReader(encodedData))
		if err != nil {
			return errors.Wrap(err, "failed to build request")
		}

		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

		err = c.doReq(ctx, req, out)
		if err != nil {
			if allowRetries {
				if errors.Is(err, io.EOF) {
					// TODO(brett19): need to deal with context deadline here
					time.Sleep(1 * time.Second)
					continue
				}
			}

			return err
		}

		return nil
	}
}

func (c *Controller) genServicesList(opts *NodeSetupOptions) string {
	var serviceNames []string
	if opts.EnableKvService {
		serviceNames = append(serviceNames, "kv")
	}
	if opts.EnableN1qlService {
		serviceNames = append(serviceNames, "n1ql")
	}
	if opts.EnableIndexService {
		serviceNames = append(serviceNames, "index")
	}
	if opts.EnableFtsService {
		serviceNames = append(serviceNames, "fts")
	}
	if opts.EnableCbasService {
		serviceNames = append(serviceNames, "cbas")
	}
	if opts.EnableEventingService {
		serviceNames = append(serviceNames, "eventing")
	}
	if opts.EnableBackupService {
		serviceNames = append(serviceNames, "backup")
	}

	return strings.Join(serviceNames, ",")
}

func (c *Controller) SetupFirstNode(ctx context.Context, opts *SetupFirstNodeOptions) error {
	err := c.doFormPost(ctx, "/nodeInit", url.Values{
		"hostname": []string{"127.0.0.1"},
		"afamily":  []string{"ipv4"},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to setup services")
	}

	err = c.doFormPost(ctx, "/pools/default", url.Values{
		"clusterName":         []string{"test-cluster"},
		"memoryQuota":         []string{fmt.Sprintf("%d", opts.KvMemoryQuotaMB)},
		"indexMemoryQuota":    []string{fmt.Sprintf("%d", opts.IndexMemoryQuotaMB)},
		"ftsMemoryQuota":      []string{fmt.Sprintf("%d", opts.FtsMemoryQuotaMB)},
		"cbasMemoryQuota":     []string{fmt.Sprintf("%d", opts.CbasMemoryQuotaMB)},
		"eventingMemoryQuota": []string{fmt.Sprintf("%d", opts.EventingMemoryQuotaMB)},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to configure memory quotas")
	}

	err = c.doFormPost(ctx, "/node/controller/setupServices", url.Values{
		"services": []string{c.genServicesList(&opts.NodeSetupOptions)},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to setup services")
	}

	err = c.doFormPost(ctx, "/node/controller/enableExternalListener", url.Values{
		"afamily":        []string{"ipv4"},
		"nodeEncryption": []string{"off"},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to enable external listener")
	}

	err = c.doFormPost(ctx, "/node/controller/setupNetConfig", url.Values{
		"afamily":        []string{"ipv4"},
		"nodeEncryption": []string{"off"},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to setup net config")
	}

	err = c.doFormPost(ctx, "/node/controller/disableUnusedExternalListeners", url.Values{}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to disable unused external listeners")
	}

	err = c.doFormPost(ctx, "/settings/indexes", url.Values{
		"storageMode": []string{"plasma"},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to setup net config")
	}

	err = c.doFormPost(ctx, "/settings/web", url.Values{
		"username": []string{opts.Username},
		"password": []string{opts.Password},
		"port":     []string{"SAME"},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to configure credentials")
	}

	return nil
}

func (c *Controller) AddNode(ctx context.Context, opts *AddNodeOptions) error {
	err := c.doFormPost(ctx, "/pools/default/serverGroups/0/addNode", url.Values{
		"hostname": []string{opts.Address},
		"services": []string{c.genServicesList(&opts.NodeSetupOptions)},
		"user":     []string{""},
		"password": []string{""},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to add node")
	}

	return nil
}

func (c *Controller) BeginRebalance(ctx context.Context) error {
	var resp struct {
		Nodes []struct {
			OTPNode string `json:"otpNode"`
		} `json:"nodes"`
	}
	err := c.doGet(ctx, "/pools/default", &resp)
	if err != nil {
		return errors.Wrap(err, "failed to fetch list of nodes to rebalance")
	}

	var otpNodes []string
	for _, node := range resp.Nodes {
		otpNodes = append(otpNodes, node.OTPNode)
	}

	err = c.doFormPost(ctx, "/controller/rebalance", url.Values{
		"knownNodes": []string{strings.Join(otpNodes, ",")},
	}, true, nil)
	if err != nil {
		return errors.Wrap(err, "failed to start rebalance")
	}

	return nil
}

func (c *Controller) WaitForNoRunningTasks(ctx context.Context) error {
	log.Printf("waiting for all cluster tasks to complete")

	for {
		var resp []struct {
			Status string `json:"status"`
		}
		err := c.doGet(ctx, "/pools/default/tasks", &resp)
		if err != nil {
			return errors.Wrap(err, "failed to fetch list of tasks")
		}

		hasRunningTask := false
		for _, task := range resp {
			if task.Status != "notRunning" {
				hasRunningTask = true
			}
		}

		if hasRunningTask {
			time.Sleep(1 * time.Second)
			continue
		}

		break
	}

	log.Printf("done!")

	return nil
}
