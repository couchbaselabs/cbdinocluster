package dockerdeploy

import "context"

func (d *Deployer) updateLoadBalancer(
	ctx context.Context,
	loadBalancerContainerId string,
	nodes []*nodeInfo,
	isColumnar bool,
	enableSsl bool,
) error {
	var addrs []string
	for _, node := range nodes {
		if !node.IsClusterNode() {
			continue
		}

		addrs = append(addrs, node.IPAddress)
	}

	return d.controller.UpdateNginxConfig(ctx, loadBalancerContainerId, addrs, enableSsl, isColumnar)
}
