package clustercontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type NodeManager struct {
	Logger   *zap.Logger
	Endpoint string
}

func (m *NodeManager) Controller() *Controller {
	return &Controller{
		Logger:   m.Logger,
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

type AnalyticsSettings struct {
	BlobStorageRegion         string
	BlobStoragePrefix         string
	BlobStorageBucket         string
	BlobStorageScheme         string
	BlobStorageEndpoint       string
	BlobStorageAnonymousAuth  bool
	BlobStorageForcePathStyle bool
}

type SetupOneNodeClusterOptions struct {
	KvMemoryQuotaMB       int
	IndexMemoryQuotaMB    int
	FtsMemoryQuotaMB      int
	CbasMemoryQuotaMB     int
	EventingMemoryQuotaMB int

	Username string
	Password string

	Services          []string
	ServerGroup       string
	AnalyticsSettings AnalyticsSettings
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

	err = c.SetupAnalytics(ctx, &SetupAnalyticsOptions{
		BlobStorageRegion:         opts.AnalyticsSettings.BlobStorageRegion,
		BlobStoragePrefix:         opts.AnalyticsSettings.BlobStoragePrefix,
		BlobStorageBucket:         opts.AnalyticsSettings.BlobStorageBucket,
		BlobStorageScheme:         opts.AnalyticsSettings.BlobStorageScheme,
		BlobStorageEndpoint:       opts.AnalyticsSettings.BlobStorageEndpoint,
		BlobStorageAnonymousAuth:  opts.AnalyticsSettings.BlobStorageAnonymousAuth,
		BlobStorageForcePathStyle: opts.AnalyticsSettings.BlobStorageForcePathStyle,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure analytics settings")
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

func formatEndpoint(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	if !strings.Contains(addr, ":") {
		return fmt.Sprintf("http://%s:8091", addr)
	}
	return fmt.Sprintf("http://%s", addr)
}

func (m *NodeManager) checkClusterIsBalanced(
	ctx context.Context,
	allNodeAddresses []string,
	ejectedNodeOtps []string,
) (bool, error) {
	numOrchestrators := 0
	for _, addr := range allNodeAddresses {
		endpoint := formatEndpoint(addr)
		nodeCtrl := &NodeManager{
			Logger:   m.Logger,
			Endpoint: endpoint,
		}

		localInfo, err := nodeCtrl.Controller().GetLocalInfo(ctx)
		if err != nil {
			// failed to contact the node, which is expected if the node was successfully ejected/shut down,
			// or if it's transiently offline.
			m.Logger.Info("failed to get local info during validation, skipping",
				zap.String("address", addr))
			continue
		}

		terseClusterInfo, err := nodeCtrl.Controller().GetTerseClusterInfo(ctx)
		if err != nil {
			m.Logger.Info("failed to get terse cluster info during validation, skipping",
				zap.String("address", addr))
			continue
		}

		if localInfo.OTPNode == "" {
			continue
		}

		isOrchestrator := terseClusterInfo.Orchestrator == localInfo.OTPNode
		if isOrchestrator {
			numOrchestrators++
		}

		if isOrchestrator && !terseClusterInfo.IsBalanced {
			m.Logger.Info("cluster still unbalanced after rebalance")
			return false, nil
		}

		if localInfo.Status != "" && localInfo.Status != "healthy" {
			m.Logger.Info("node unhealthy after rebalance", zap.String("node", localInfo.OTPNode))
			return false, nil
		}

		// If this node is still in the cluster but was supposed to be ejected
		for _, ejectedOtp := range ejectedNodeOtps {
			if ejectedOtp == localInfo.OTPNode {
				m.Logger.Info("node unexpectedly still present after rebalance", zap.String("node", localInfo.OTPNode))
				return false, nil
			}
		}
	}

	if numOrchestrators != 1 {
		m.Logger.Info("unexpected number of orchestrators after rebalance",
			zap.Int("num_orchestrators", numOrchestrators))
		return false, nil
	}

	return true, nil
}

func (m *NodeManager) RebalanceWithRetry(
	ctx context.Context,
	allNodeAddresses []string,
	ejectedNodeOtps []string,
	lastAllowedRetryTime time.Time,
) error {
	if time.Now().After(lastAllowedRetryTime) {
		return errors.New("exhausted retry time for rebalance operation")
	}

	var ctrlNodeAddr string
	for _, addr := range allNodeAddresses {
		endpoint := formatEndpoint(addr)
		nodeCtrl := &NodeManager{
			Logger:   m.Logger,
			Endpoint: endpoint,
		}

		localInfo, err := nodeCtrl.Controller().GetLocalInfo(ctx)
		if err != nil {
			m.Logger.Info("failed to get local info for node selection, skipping",
				zap.String("address", addr),
				zap.Error(err))
			continue
		}

		if localInfo.OTPNode == "" {
			continue
		}

		// Check if this node is to be ejected
		isEjected := false
		for _, ejectedOtp := range ejectedNodeOtps {
			if ejectedOtp == localInfo.OTPNode {
				isEjected = true
				break
			}
		}

		if !isEjected {
			ctrlNodeAddr = addr
			break
		}
	}

	if ctrlNodeAddr == "" {
		return errors.New("failed to find a healthy control node that is not being ejected")
	}

	m.Logger.Debug("selected control node for rebalance operation",
		zap.String("address", ctrlNodeAddr))

	ctrlEndpoint := formatEndpoint(ctrlNodeAddr)
	ctrlNodeMgr := &NodeManager{
		Logger:   m.Logger,
		Endpoint: ctrlEndpoint,
	}

	m.Logger.Info("initiating rebalance")
	err := ctrlNodeMgr.Rebalance(ctx, ejectedNodeOtps)
	if err != nil {
		return errors.Wrap(err, "failed to start rebalance")
	}

	m.Logger.Info("waiting for rebalance completion")
	err = ctrlNodeMgr.WaitForNoRunningTasks(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to wait for tasks to complete")
	}

	m.Logger.Info("validating post-rebalance state")

	startTime := time.Now()
	for {
		if time.Since(startTime) >= 15*time.Second {
			allowedTimeLeft := time.Until(lastAllowedRetryTime)
			m.Logger.Info("cluster still not balanced, assuming failure and retrying", zap.Duration("time_left", allowedTimeLeft))

			var newOtpsToRemove []string
			if len(ejectedNodeOtps) > 0 {
				// Filter out any ejectedNodeOtps that are no longer actually in the cluster.
				allActiveOtps, err := ctrlNodeMgr.Controller().ListNodeOTPs(ctx)
				if err != nil {
					return errors.Wrap(err, "failed to list node otps during retry filtering")
				}

				for _, ejectedOtp := range ejectedNodeOtps {
					found := false
					for _, activeOtp := range allActiveOtps {
						if activeOtp == ejectedOtp {
							found = true
							break
						}
					}
					if !found {
						m.Logger.Info("node to remove not found in actual cluster, skipping", zap.String("node", ejectedOtp))
						continue
					}
					newOtpsToRemove = append(newOtpsToRemove, ejectedOtp)
				}
			}

			return ctrlNodeMgr.RebalanceWithRetry(ctx, allNodeAddresses, newOtpsToRemove, lastAllowedRetryTime)
		}

		clusterIsBalanced, err := ctrlNodeMgr.checkClusterIsBalanced(ctx, allNodeAddresses, ejectedNodeOtps)
		if err != nil {
			return errors.Wrap(err, "failed to validate cluster state after rebalance")
		}

		if !clusterIsBalanced {
			// m.Logger.Info("cluster not balanced after rebalance, waiting 1 second to re-validate")
			select {
			case <-time.After(1 * time.Second):
				// continue
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		break
	}

	return nil
}
