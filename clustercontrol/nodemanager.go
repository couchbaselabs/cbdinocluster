package clustercontrol

import (
	"context"
	"time"

	"github.com/pkg/errors"
)

type NodeManager struct {
	Endpoint string
}

func (m *NodeManager) Controller() *Controller {
	return &Controller{
		Endpoint: m.Endpoint,
	}
}

func (m *NodeManager) WaitForOnline(ctx context.Context) error {
	c := m.Controller()

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

type NodeSetupOptions struct {
	EnableKvService       bool
	EnableN1qlService     bool
	EnableIndexService    bool
	EnableFtsService      bool
	EnableCbasService     bool
	EnableEventingService bool
	EnableBackupService   bool
}

func (o NodeSetupOptions) ServicesList() []string {
	var serviceNames []string
	if o.EnableKvService {
		serviceNames = append(serviceNames, "kv")
	}
	if o.EnableN1qlService {
		serviceNames = append(serviceNames, "query")
	}
	if o.EnableIndexService {
		serviceNames = append(serviceNames, "index")
	}
	if o.EnableFtsService {
		serviceNames = append(serviceNames, "fts")
	}
	if o.EnableCbasService {
		serviceNames = append(serviceNames, "cbas")
	}
	if o.EnableEventingService {
		serviceNames = append(serviceNames, "eventing")
	}
	if o.EnableBackupService {
		serviceNames = append(serviceNames, "backup")
	}
	return serviceNames
}

func (o NodeSetupOptions) NsServicesList() []string {
	var serviceNames []string
	if o.EnableKvService {
		serviceNames = append(serviceNames, "kv")
	}
	if o.EnableN1qlService {
		serviceNames = append(serviceNames, "n1ql")
	}
	if o.EnableIndexService {
		serviceNames = append(serviceNames, "index")
	}
	if o.EnableFtsService {
		serviceNames = append(serviceNames, "fts")
	}
	if o.EnableCbasService {
		serviceNames = append(serviceNames, "cbas")
	}
	if o.EnableEventingService {
		serviceNames = append(serviceNames, "eventing")
	}
	if o.EnableBackupService {
		serviceNames = append(serviceNames, "backup")
	}
	return serviceNames
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
	c := m.Controller()

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
	c := m.Controller()

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
	c := m.Controller()

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
