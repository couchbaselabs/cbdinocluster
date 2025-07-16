package dockerdeploy

import "context"

func proxyTargetsFromNodeInfos(nodes []*nodeInfo) []ProxyTargetNode {
	var targets []ProxyTargetNode
	for _, node := range nodes {
		if !node.IsClusterNode() {
			continue
		}

		targets = append(targets, ProxyTargetNode{
			Address:               node.IPAddress,
			IsEnterpriseAnalytics: isColumnarVersionEA(node.InitialServerVersion),
		})
	}
	return targets
}

func (d *Deployer) updatePassiveLoadBalancer(
	ctx context.Context,
	loadBalancerContainerId string,
	nodes []*nodeInfo,
	isColumnar bool,
	enableSsl bool,
) error {
	targets := proxyTargetsFromNodeInfos(nodes)
	return d.controller.UpdateNginxConfig(ctx, loadBalancerContainerId, targets, enableSsl, isColumnar)
}

func (d *Deployer) updateActiveLoadBalancer(
	ctx context.Context,
	loadBalancerContainerId string,
	nodes []*nodeInfo,
	enableSsl bool,
	isColumnar bool,
) error {
	targets := proxyTargetsFromNodeInfos(nodes)
	return d.controller.UpdateHaproxyConfig(ctx, loadBalancerContainerId, targets, enableSsl, isColumnar)
}
