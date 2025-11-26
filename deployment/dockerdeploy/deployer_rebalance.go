package dockerdeploy

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (d *Deployer) reconcileRebalance(
	ctx context.Context,
	clusterInfoEx *clusterInfoEx,
	otpsToRemove []string,
) error {
	// we keep retrying to rebalance until more than 15 minutes have passed
	lastAllowedRetryTime := time.Now().Add(15 * time.Minute)
	return d.reconcileRebalanceWithRetry(ctx, clusterInfoEx, otpsToRemove, lastAllowedRetryTime)
}

func (d *Deployer) checkClusterIsBalanced(
	ctx context.Context,
	clusterInfo *clusterInfo,
	otpsToRemove []string,
) (bool, error) {
	clusterInfoEx, err := d.getClusterInfoEx(ctx, clusterInfo)
	if err != nil {
		return false, errors.Wrap(err, "failed to get extended cluster info for rebalance")
	}

	numOrchestrators := 0
	for _, node := range clusterInfoEx.NodesEx {
		if node.IsClusterOrchestrator {
			numOrchestrators++
		}
	}
	if numOrchestrators != 1 {
		// if nobody is advertising as being the orchestrator or more than one node is advertising
		// as being the orchestrator then something went wrong, and we can just try the rebalance
		// again and loop back around...
		d.logger.Info("unexpected number of orchestrators after rebalance",
			zap.Int("num_orchestrators", numOrchestrators))
		return false, nil
	}

	for _, node := range clusterInfoEx.NodesEx {
		if node.OTPNode == "" {
			// no OTPNode info means it is probably not actually in the cluster
			continue
		}

		if node.IsClusterOrchestrator && node.ClusterNeedsRebalance {
			d.logger.Info("cluster still needs rebalance after rebalance operation")
			return false, nil
		}

		if node.Status != "" && node.Status != "healthy" {
			d.logger.Info("node unhealthy after rebalance", zap.String("node", node.OTPNode))
			return false, nil
		}

		if slices.Contains(otpsToRemove, node.OTPNode) {
			d.logger.Info("node unexpectedly still present after rebalance", zap.String("node", node.OTPNode))
			return false, nil
		}
	}

	return true, nil
}

func (d *Deployer) reconcileRebalanceWithRetry(
	ctx context.Context,
	clusterInfoEx *clusterInfoEx,
	otpsToRemove []string,
	lastAllowedRetryTime time.Time,
) error {
	if time.Now().After(lastAllowedRetryTime) {
		return errors.New("exhausted retry time for rebalance operation")
	}

	// we need to fetch the most up to date information about the cluster
	clusterInfo, err := d.getCluster(ctx, clusterInfoEx.ClusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info for rebalance")
	}

	clusterInfoEx, err = d.getClusterInfoEx(ctx, clusterInfo)
	if err != nil {
		return errors.Wrap(err, "failed to get extended cluster info for rebalance")
	}

	// once all the new nodes are registered, we re-select a node to work with that is
	// not being removed from the cluster, which can now include the new nodes...
	var ctrlNode *nodeInfoEx
	for _, clusterNode := range clusterInfoEx.NodesEx {
		if !clusterNode.IsClusterNode() {
			continue
		}

		if clusterNode.OTPNode == "" {
			// no OTPNode info means it is probably not actually in the cluster
			continue
		}

		if !slices.Contains(otpsToRemove, clusterNode.OTPNode) {
			ctrlNode = clusterNode
		}
	}

	if ctrlNode == nil {
		return errors.New("failed to find control node for rebalance")
	}

	d.logger.Debug("selected node for rebalance operation",
		zap.String("address", ctrlNode.IPAddress))

	nodeCtrl := clustercontrol.NodeManager{
		Logger:   d.logger,
		Endpoint: fmt.Sprintf("http://%s:8091", ctrlNode.IPAddress),
	}

	d.logger.Info("initiating rebalance")

	err = nodeCtrl.Rebalance(ctx, otpsToRemove)
	if err != nil {
		return errors.Wrap(err, "failed to start rebalance")
	}

	d.logger.Info("waiting for rebalance completion")

	err = nodeCtrl.WaitForNoRunningTasks(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to wait for tasks to complete")
	}

	d.logger.Info("validating post-rebalance state")

	clusterIsBalanced, err := d.checkClusterIsBalanced(ctx, clusterInfo, otpsToRemove)
	if err != nil {
		return errors.Wrap(err, "failed to validate cluster state after rebalance")
	}

	if !clusterIsBalanced {
		// if the cluster is not balanced immediately after the rebalance, we wait 15 seconds
		// and check again to see if the state was just stale.  If it still needs a rebalance,
		// we trigger another rebalance to try and resolve the issue.
		d.logger.Info("cluster not balanced after rebalance, waiting 15 seconds to re-validate")
		time.Sleep(15 * time.Second)

		clusterIsBalanced, err = d.checkClusterIsBalanced(ctx, clusterInfo, otpsToRemove)
		if err != nil {
			return errors.Wrap(err, "failed to validate cluster state after rebalance")
		}

		if !clusterIsBalanced {
			allowedTimeLeft := time.Until(lastAllowedRetryTime)
			d.logger.Info("cluster still not balanced, assuming failure and retrying", zap.Duration("time_left", allowedTimeLeft))

			allNodeOtps := make([]string, 0)
			for _, clusterNode := range clusterInfoEx.NodesEx {
				if !clusterNode.IsClusterNode() {
					continue
				}

				if clusterNode.OTPNode == "" {
					// no OTPNode info means its probably not actually in the cluster
					continue
				}

				allNodeOtps = append(allNodeOtps, clusterNode.OTPNode)
			}

			// if we have any nodes to remove that are not actually in the cluster we skip them
			var newOtpsToRemove []string
			for _, otpToRemove := range otpsToRemove {
				if !slices.Contains(allNodeOtps, otpToRemove) {
					d.logger.Info("node to remove not found in actual cluster, skipping", zap.String("node", otpToRemove))
					continue
				}

				newOtpsToRemove = append(newOtpsToRemove, otpToRemove)
			}

			return d.reconcileRebalanceWithRetry(ctx, clusterInfoEx, newOtpsToRemove, lastAllowedRetryTime)
		}
	}

	return nil
}
