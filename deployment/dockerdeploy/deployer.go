package dockerdeploy

import (
	"context"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

type Deployer struct {
	logger        *zap.Logger
	dockerCli     *client.Client
	imageProvider ImageProvider
	controller    *Controller
}

var _ deployment.Deployer = (*Deployer)(nil)

type DeployerOptions struct {
	Logger       *zap.Logger
	DockerCli    *client.Client
	NetworkName  string
	GhcrUsername string
	GhcrPassword string
}

func NewDeployer(opts *DeployerOptions) (*Deployer, error) {
	return &Deployer{
		logger:    opts.Logger,
		dockerCli: opts.DockerCli,
		imageProvider: &HybridImageProvider{
			Logger:       opts.Logger,
			DockerCli:    opts.DockerCli,
			GhcrUsername: opts.GhcrUsername,
			GhcrPassword: opts.GhcrPassword,
		},
		controller: &Controller{
			Logger:      opts.Logger,
			DockerCli:   opts.DockerCli,
			NetworkName: opts.NetworkName,
		},
	}, nil
}

func (d *Deployer) ListClusters(ctx context.Context) ([]*deployment.ClusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	// sort the nodes by their name for nicer printing later
	slices.SortFunc(nodes, func(a *NodeInfo, b *NodeInfo) bool {
		return strings.Compare(a.Name, b.Name) < 0
	})

	var clusters []*deployment.ClusterInfo
	getCluster := func(clusterID string) *deployment.ClusterInfo {
		for _, cluster := range clusters {
			if cluster.ClusterID == clusterID {
				return cluster
			}
		}
		cluster := &deployment.ClusterInfo{
			ClusterID: clusterID,
		}
		clusters = append(clusters, cluster)
		return cluster
	}

	for _, node := range nodes {
		cluster := getCluster(node.ClusterID)
		cluster.Creator = node.Creator
		cluster.Owner = node.Owner
		cluster.Purpose = node.Purpose
		if node.Expiry.After(cluster.Expiry) {
			cluster.Expiry = node.Expiry
		}
		cluster.Nodes = append(cluster.Nodes, &deployment.ClusterNodeInfo{
			ResourceID: "docker:" + node.ContainerID[0:8] + "...",
			NodeID:     node.NodeID,
			Name:       node.Name,
			IPAddress:  node.IPAddress,
		})
	}

	return clusters, nil
}

func (d *Deployer) NewCluster(ctx context.Context, opts *deployment.NewClusterOptions) (*deployment.ClusterInfo, error) {
	for _, node := range opts.Nodes {
		if node.Name == "" {
			return nil, errors.New("all defined nodes must have names")
		}
	}

	d.logger.Info("gathering node images")

	nodeDefs := make([]*ImageDef, len(opts.Nodes))
	nodeImages := make([]*ImageRef, len(opts.Nodes))
	for nodeIdx, node := range opts.Nodes {
		imageDef := &ImageDef{
			Version:             node.Version,
			BuildNo:             node.BuildNo,
			UseCommunityEdition: node.UseCommunityEdition,
			UseServerless:       node.UseServerless,
		}
		nodeDefs[nodeIdx] = imageDef

		var imageRef *ImageRef
		for oNodeIdx := 0; oNodeIdx < nodeIdx; oNodeIdx++ {
			if CompareImageDefs(nodeDefs[oNodeIdx], imageDef) == 0 {
				imageRef = nodeImages[oNodeIdx]
			}
		}

		if imageRef == nil {
			foundImageRef, err := d.imageProvider.GetImage(ctx, imageDef)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get image for a node")
			}

			imageRef = foundImageRef
		}

		nodeImages[nodeIdx] = imageRef
	}

	clusterID := uuid.NewString()

	d.logger.Info("deploying nodes")

	nodes := make([]*NodeInfo, len(opts.Nodes))
	leaveNodesAfterReturn := false
	cleanupNodes := func() {
		if !leaveNodesAfterReturn {
			for _, node := range nodes {
				if node != nil {
					d.controller.RemoveNode(ctx, node.ContainerID)
				}
			}
		}
	}
	defer cleanupNodes()

	waitCh := make(chan error)
	for nodeIdx, nodeDef := range opts.Nodes {
		go func(nodeIdx int, nodeDef *deployment.NewClusterNodeOptions) {
			logger := d.logger.With(zap.Int("index", nodeIdx))
			logger.Info("deploying node", zap.Any("def", nodeDef))

			node, err := d.controller.DeployNode(ctx, &DeployNodeOptions{
				Creator:   opts.Creator,
				Name:      nodeDef.Name,
				Purpose:   opts.Purpose,
				ClusterID: clusterID,
				Image:     nodeImages[nodeIdx],
				Expiry:    opts.Expiry,
			})
			if err != nil {
				waitCh <- errors.Wrap(err, "failed to deploy a node")
				return
			}

			logger.Info("deployed node")

			nodes[nodeIdx] = node
			waitCh <- nil
		}(nodeIdx, nodeDef)
	}
	for range opts.Nodes {
		err := <-waitCh
		if err != nil {
			return nil, err
		}
	}

	d.logger.Info("nodes deployed")

	// we cheat for now...
	clusters, err := d.ListClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}

	var thisCluster *deployment.ClusterInfo
	for _, cluster := range clusters {
		if cluster.ClusterID == clusterID {
			thisCluster = cluster
		}
	}
	if thisCluster == nil {
		return nil, errors.New("failed to find new cluster after deployment")
	}

	leaveNodesAfterReturn = true
	return thisCluster, nil
}

func (d *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		if node.ClusterID == clusterID {
			d.logger.Info("removing node",
				zap.String("id", node.NodeID),
				zap.String("container", node.ContainerID))

			d.controller.RemoveNode(ctx, node.ContainerID)
		}
	}

	return nil
}

func (d *Deployer) RemoveAll(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	for _, node := range nodes {
		d.logger.Info("removing node",
			zap.String("id", node.NodeID),
			zap.String("container", node.ContainerID))

		d.controller.RemoveNode(ctx, node.ContainerID)
	}

	return nil
}

func (d *Deployer) Cleanup(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	curTime := time.Now()
	for _, node := range nodes {
		if !node.Expiry.After(curTime) {
			d.logger.Info("removing node",
				zap.String("id", node.NodeID),
				zap.String("container", node.ContainerID))

			d.controller.RemoveNode(ctx, node.ContainerID)
		}
	}

	return nil
}

func (d *Deployer) DestroyAllResources(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list all nodes")
	}

	for _, node := range nodes {
		d.logger.Info("removing node",
			zap.String("id", node.NodeID),
			zap.String("container", node.ContainerID))

		err := d.controller.RemoveNode(ctx, node.ContainerID)
		if err != nil {
			return errors.Wrap(err, "failed to remove")
		}
	}

	return nil
}
