package cbmgmtrest

import (
	"context"
	"time"

	"github.com/pkg/errors"
)

type NodeManager struct {
	Endpoint string
	Username string
	Password string
}

func (m *NodeManager) Client() *Client {
	return &Client{
		Endpoint: m.Endpoint,
		Username: m.Username,
		Password: m.Password,
	}
}

func (m *NodeManager) WaitForOnline(ctx context.Context) error {
	c := m.Client()

	for {
		err := c.Ping(ctx)
		if err != nil {
			select {
			case <-time.After(1 * time.Second):
				// continue
			case <-ctx.Done():
				return errors.Wrap(ctx.Err(), "context finished while waiting for node to start")
			}
			continue
		}

		break
	}

	return nil
}

type SetupOneNodeClusterOptions struct {
	KvMemoryQuotaMB       int
	IndexMemoryQuotaMB    int
	FtsMemoryQuotaMB      int
	CbasMemoryQuotaMB     int
	EventingMemoryQuotaMB int

	Username string
	Password string

	NodeSetupOptions
}

func (m *NodeManager) SetupOneNodeCluster(ctx context.Context, opts *SetupOneNodeClusterOptions) error {
	c := m.Client()

	err := c.NodeInit(ctx, &NodeInitOptions{
		Hostname: "127.0.0.1",
		Afamily:  "ipv4",
	})
	if err != nil {
		return errors.Wrap(err, "failed to setup services")
	}

	err = c.UpdateDefaultPool(ctx, &UpdateDefaultPoolOptions{
		ClusterName:           "test-cluster",
		KvMemoryQuotaMB:       opts.KvMemoryQuotaMB,
		IndexMemoryQuotaMB:    opts.IndexMemoryQuotaMB,
		FtsMemoryQuotaMB:      opts.FtsMemoryQuotaMB,
		CbasMemoryQuotaMB:     opts.CbasMemoryQuotaMB,
		EventingMemoryQuotaMB: opts.EventingMemoryQuotaMB,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure memory quotas")
	}

	err = c.SetupServices(ctx, &SetupServicesOptions{
		Services: opts.NodeSetupOptions.NsServicesList(),
	})
	if err != nil {
		return errors.Wrap(err, "failed to setup services")
	}

	err = c.EnableExternalListener(ctx, &EnableExternalListenerOptions{
		Afamily:        "ipv4",
		NodeEncryption: "off",
	})
	if err != nil {
		return errors.Wrap(err, "failed to enable external listener")
	}

	err = c.SetupNetConfig(ctx, &SetupNetConfigOptions{
		Afamily:        "ipv4",
		NodeEncryption: "off",
	})
	if err != nil {
		return errors.Wrap(err, "failed to setup net config")
	}

	err = c.DisableUnusedExternalListeners(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to disable unused external listeners")
	}

	err = c.UpdateIndexSettings(ctx, &UpdateIndexSettingsOptions{
		StorageMode: "plasma",
	})
	if err != nil {
		return errors.Wrap(err, "failed to setup net config")
	}

	err = c.UpdateWebSettings(ctx, &UpdateWebSettingsOptions{
		Username: opts.Username,
		Password: opts.Password,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure credentials")
	}

	return nil
}

func (m *NodeManager) Rebalance(ctx context.Context) error {
	c := m.Client()

	nodeOtps, err := c.ListNodeOTPs(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list node otps")
	}

	err = c.BeginRebalance(ctx, &BeginRebalanceOptions{
		KnownNodeOTPs: nodeOtps,
	})
	if err != nil {
		return errors.Wrap(err, "failed to start rebalance")
	}

	return nil
}

func (m *NodeManager) WaitForNoRunningTasks(ctx context.Context) error {
	c := m.Client()

	for {
		tasks, err := c.ListTasks(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch list of tasks")
		}

		hasRunningTask := false
		for _, task := range tasks {
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

	return nil
}
