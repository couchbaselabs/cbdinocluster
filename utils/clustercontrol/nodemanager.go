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

type SetupOneNodeClusterOptions struct {
	KvMemoryQuotaMB       int
	IndexMemoryQuotaMB    int
	FtsMemoryQuotaMB      int
	CbasMemoryQuotaMB     int
	EventingMemoryQuotaMB int

	Username string
	Password string

	Services    []string
	ServerGroup string
}

func (m *NodeManager) SetupOneNodeCluster(ctx context.Context, opts *SetupOneNodeClusterOptions) error {
	c := m.Controller()

	// While Couchbase Server 7.0+ seems to invoke this as part of cluster initialization
	// it does not appear to be neccessary for a properly functioning cluster, and it is
	// not supported on 6.6 and before, so it's just disabled here.
	/*
		err := c.NodeInit(ctx, &NodeInitOptions{
			Hostname: "127.0.0.1",
			Afamily:  "ipv4",
		})
		if err != nil {
			return errors.Wrap(err, "failed to perform nodeInit")
		}
	*/

	err := c.UpdateDefaultPool(ctx, &UpdateDefaultPoolOptions{
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
		Services: opts.Services,
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
		err = c.UpdateIndexSettings(ctx, &UpdateIndexSettingsOptions{
			StorageMode: "forestdb",
		})
		if err != nil {
			return errors.Wrap(err, "failed to setup net config")
		}
	}

	err = c.UpdateWebSettings(ctx, &UpdateWebSettingsOptions{
		Username: opts.Username,
		Password: opts.Password,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure credentials")
	}

	if opts.ServerGroup != "" {
		// Just rename default server group for the first node
		err = c.RenameServerGroup(ctx, &RenameServerGroupOptions{
			GroupUUID: "0",
			Name:      opts.ServerGroup,
		})
		if err != nil {
			return errors.Wrap(err, "failed to rename default server group")
		}
	}

	return nil
}

func (m *NodeManager) Rebalance(ctx context.Context, ejectedNodeOtps []string) error {
	c := m.Controller()

	nodeOtps, err := c.ListNodeOTPs(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list node otps")
	}

	err = c.BeginRebalance(ctx, &BeginRebalanceOptions{
		KnownNodeOTPs:   nodeOtps,
		EjectedNodeOTPs: ejectedNodeOtps,
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
			taskStatus := task.GetStatus()
			if taskStatus != "notRunning" &&
				taskStatus != "completed" &&
				taskStatus != "cancelled" {
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

func (m *NodeManager) WaitForTaskRunning(ctx context.Context, taskType string) error {
	c := m.Controller()

	for {
		tasks, err := c.ListTasks(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to fetch list of tasks")
		}

		hasRunningTask := false
		for _, task := range tasks {
			if task.GetType() == taskType && task.GetStatus() == "running" {
				hasRunningTask = true
			}
		}

		if !hasRunningTask {
			time.Sleep(1 * time.Second)
			continue
		}

		break
	}

	return nil
}

// waits for log collection and returns a map of otp -> log path
func (m *NodeManager) WaitForLogCollection(ctx context.Context) (map[string]string, error) {
	c := m.Controller()

	for {
		tasks, err := c.ListTasks(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch list of tasks")
		}

		var foundLogTask *CollectLogsTask
		for _, task := range tasks {
			if logTask, ok := task.(CollectLogsTask); ok {
				foundLogTask = &logTask
			}
		}
		if foundLogTask == nil {
			return nil, errors.New("failed to find log collection task")
		}

		if foundLogTask.Status != "completed" {
			time.Sleep(1 * time.Second)
			continue
		}

		paths := make(map[string]string)
		for nodeId, nodeInfo := range foundLogTask.PerNode {
			paths[nodeId] = nodeInfo.Path
		}

		return paths, nil
	}
}
