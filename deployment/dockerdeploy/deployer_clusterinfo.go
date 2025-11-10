package dockerdeploy

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type clusterInfo struct {
	ClusterID      string
	Type           deployment.ClusterType
	Creator        string
	Owner          string
	Purpose        string
	Expiry         time.Time
	DnsName        string
	UsingDinoCerts bool
	Nodes          []*nodeInfo
}

func (c clusterInfo) IsColumnar() bool {
	return c.Type == deployment.ClusterTypeColumnar
}

func (c clusterInfo) LoadBalancerIPAddress() string {
	for _, node := range c.Nodes {
		if node.IsActiveLoadBalancerNode() {
			return node.IPAddress
		}
	}
	for _, node := range c.Nodes {
		if node.IsPassiveLoadBalancerNode() {
			return node.IPAddress
		}
	}
	return ""
}

type nodeInfo struct {
	NodeID               string
	Type                 string
	Name                 string
	ContainerID          string
	IPAddress            string
	DnsName              string
	InitialServerVersion string
}

func (i nodeInfo) IsClusterNode() bool {
	return i.Type == "server-node" || i.Type == "columnar-node"
}

func (i nodeInfo) IsColumnarNode() bool {
	return i.Type == "columnar-node"
}

func (i nodeInfo) IsColumnarNodeEA() bool {
	if !i.IsColumnarNode() {
		return false
	}
	return isColumnarVersionEA(i.InitialServerVersion)
}

func (i nodeInfo) IsPassiveLoadBalancerNode() bool {
	return i.Type == "nginx"
}

func (i nodeInfo) IsActiveLoadBalancerNode() bool {
	return i.Type == "haproxy"
}

func (d *Deployer) listClusters(ctx context.Context) ([]*clusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	// sort the nodes by their name for nicer printing later
	slices.SortFunc(nodes, func(a *ContainerInfo, b *ContainerInfo) int {
		return strings.Compare(a.Name, b.Name)
	})

	var clusters []*clusterInfo
	getCluster := func(clusterID string) *clusterInfo {
		for _, cluster := range clusters {
			if cluster.ClusterID == clusterID {
				return cluster
			}
		}
		cluster := &clusterInfo{
			ClusterID: clusterID,
			Type:      deployment.ClusterTypeServer,
		}
		clusters = append(clusters, cluster)
		return cluster
	}

	for _, node := range nodes {
		cluster := getCluster(node.ClusterID)

		nodeInfo := &nodeInfo{
			NodeID:               node.NodeID,
			Type:                 node.Type,
			Name:                 node.Name,
			ContainerID:          node.ContainerID,
			IPAddress:            node.IPAddress,
			DnsName:              node.DnsName,
			InitialServerVersion: node.InitialServerVersion,
		}
		cluster.Nodes = append(cluster.Nodes, nodeInfo)

		if nodeInfo.IsClusterNode() {
			cluster.Creator = node.Creator
			cluster.Owner = node.Owner
			cluster.Purpose = node.Purpose
			if !node.Expiry.IsZero() && node.Expiry.After(cluster.Expiry) {
				cluster.Expiry = node.Expiry
			}
			cluster.DnsName = node.DnsSuffix
			cluster.UsingDinoCerts = node.UsingDinoCerts
		}

		// if any nodes are columnar nodes, the cluster is a columnar cluster
		if nodeInfo.IsColumnarNode() {
			cluster.Type = deployment.ClusterTypeColumnar
		}
	}

	return clusters, nil
}

func (d *Deployer) getCluster(ctx context.Context, clusterID string) (*clusterInfo, error) {
	clusters, err := d.listClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var thisCluster *clusterInfo
	for _, cluster := range clusters {
		if cluster.ClusterID == clusterID {
			thisCluster = cluster
		}
	}
	if thisCluster == nil {
		return nil, errors.New("failed to find cluster")
	}

	return thisCluster, nil
}

type clusterInfoEx struct {
	clusterInfo

	NodesEx []*nodeInfoEx
}

type nodeInfoEx struct {
	nodeInfo

	Status                string
	OTPNode               string
	Services              []clusterdef.Service
	ClusterNeedsRebalance bool
}

func (d *Deployer) getNodeInfoEx(ctx context.Context, nodeInfo *nodeInfo) (*nodeInfoEx, error) {
	nodeEx := &nodeInfoEx{
		nodeInfo: *nodeInfo,
	}

	if !nodeInfo.IsClusterNode() {
		return nodeEx, nil
	}

	nodeCtrl := clustercontrol.NodeManager{
		Logger:   d.logger,
		Endpoint: fmt.Sprintf("http://%s:8091", nodeInfo.IPAddress),
	}
	thisNodeInfo, err := nodeCtrl.Controller().GetLocalInfo(ctx)
	if err != nil {
		// there are cases where we want to fetch extended cluster information while
		// one of the nodes will not respond to this endpoint so we consider this non-fatal
		d.logger.Info("failed to get extended node info, skipping",
			zap.String("node", nodeInfo.Name))
		return nodeEx, nil
	}

	services, err := clusterdef.NsServicesToServices(thisNodeInfo.Services)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate services list")
	}

	nodeEx.Status = thisNodeInfo.Status
	nodeEx.OTPNode = thisNodeInfo.OTPNode
	nodeEx.Services = services
	nodeEx.ClusterNeedsRebalance = thisNodeInfo.ClusterNeedsRebalance

	return nodeEx, nil
}

func (d *Deployer) getClusterInfoEx(ctx context.Context, clusterInfo *clusterInfo) (*clusterInfoEx, error) {
	cluster := &clusterInfoEx{
		clusterInfo: *clusterInfo,
	}

	for _, node := range cluster.Nodes {
		nodeEx, err := d.getNodeInfoEx(ctx, node)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get extended node info")
		}

		cluster.NodesEx = append(cluster.NodesEx, nodeEx)
	}

	return cluster, nil
}

func (d *Deployer) nodeInfoFromNode(node *nodeInfo) deployment.ClusterNodeInfo {
	return &ClusterNodeInfo{
		NodeID:     node.NodeID,
		IsNode:     node.IsClusterNode(),
		Name:       node.Name,
		ResourceID: node.ContainerID[0:8] + "...",
		IPAddress:  node.IPAddress,
	}
}

func (d *Deployer) clusterInfoFromCluster(cluster *clusterInfo) deployment.ClusterInfo {
	var nodes []deployment.ClusterNodeInfo
	for _, node := range cluster.Nodes {
		nodes = append(nodes, d.nodeInfoFromNode(node))
	}

	return &ClusterInfo{
		ClusterID: cluster.ClusterID,
		Type:      cluster.Type,
		Creator:   cluster.Creator,
		Owner:     cluster.Owner,
		Purpose:   cluster.Purpose,
		Expiry:    cluster.Expiry,
		Nodes:     nodes,
	}
}
