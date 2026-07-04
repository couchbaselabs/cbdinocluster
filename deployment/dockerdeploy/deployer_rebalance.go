package dockerdeploy

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/pkg/errors"
)

func (d *Deployer) reconcileRebalance(
	ctx context.Context,
	clusterInfoEx *clusterInfoEx,
	otpsToRemove []string,
) error {
	var allNodeAddresses []string
	for _, node := range clusterInfoEx.NodesEx {
		if !node.IsClusterNode() {
			continue
		}
		allNodeAddresses = append(allNodeAddresses, node.IPAddress)
	}

	// Find an initial control node that is not being removed
	var initialCtrlNode *nodeInfoEx
	for _, node := range clusterInfoEx.NodesEx {
		if !node.IsClusterNode() {
			continue
		}
		if node.OTPNode == "" {
			continue
		}
		if !slices.Contains(otpsToRemove, node.OTPNode) {
			initialCtrlNode = node
		}
	}
	if initialCtrlNode == nil {
		return errors.New("failed to find initial control node for rebalance")
	}

	nodeCtrl := clustercontrol.NodeManager{
		Logger:   d.logger,
		Endpoint: fmt.Sprintf("http://%s:8091", initialCtrlNode.IPAddress),
	}

	lastAllowedRetryTime := time.Now().Add(15 * time.Minute)
	return nodeCtrl.RebalanceWithRetry(ctx, allNodeAddresses, otpsToRemove, lastAllowedRetryTime)
}
