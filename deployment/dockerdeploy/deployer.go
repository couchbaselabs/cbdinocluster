package dockerdeploy

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/couchbase/gocbcorex"
	"github.com/couchbase/gocbcorex/cbmgmtx"
	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/exp/slices"
	"golang.org/x/mod/semver"
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

func (d *Deployer) listClusters(ctx context.Context) ([]*ClusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	// sort the nodes by their name for nicer printing later
	slices.SortFunc(nodes, func(a *NodeInfo, b *NodeInfo) bool {
		return strings.Compare(a.Name, b.Name) < 0
	})

	var clusters []*ClusterInfo
	getCluster := func(clusterID string) *ClusterInfo {
		for _, cluster := range clusters {
			if cluster.ClusterID == clusterID {
				return cluster
			}
		}
		cluster := &ClusterInfo{
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
		if !node.Expiry.IsZero() && node.Expiry.After(cluster.Expiry) {
			cluster.Expiry = node.Expiry
		}
		cluster.Nodes = append(cluster.Nodes, &ClusterNodeInfo{
			ResourceID: node.ContainerID[0:8] + "...",
			NodeID:     node.NodeID,
			Name:       node.Name,
			IPAddress:  node.IPAddress,
		})
	}

	return clusters, nil
}

func (d *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	clusters, err := d.listClusters(ctx)
	if err != nil {
		return nil, err
	}

	var out []deployment.ClusterInfo
	for _, cluster := range clusters {
		out = append(out, cluster)
	}
	return out, nil
}

func (d *Deployer) getImagesForNodeGrps(ctx context.Context, nodeGrps []*clusterdef.NodeGroup) ([]*ImageRef, error) {
	nodeGrpDefs := make([]*ImageDef, len(nodeGrps))
	nodeGrpImages := make([]*ImageRef, len(nodeGrps))
	for nodeGrpIdx, nodeGrp := range nodeGrps {
		versionInfo, err := versionident.Identify(ctx, nodeGrp.Version)
		if err != nil {
			return nil, errors.Wrap(err, "failed to identify version")
		}

		imageDef := &ImageDef{
			Version:             versionInfo.Version,
			BuildNo:             versionInfo.BuildNo,
			UseCommunityEdition: versionInfo.CommunityEdition,
			UseServerless:       versionInfo.Serverless,
		}
		nodeGrpDefs[nodeGrpIdx] = imageDef

		var imageRef *ImageRef
		for oNodeGrpIdx := 0; oNodeGrpIdx < nodeGrpIdx; oNodeGrpIdx++ {
			if CompareImageDefs(nodeGrpDefs[oNodeGrpIdx], imageDef) == 0 {
				imageRef = nodeGrpImages[oNodeGrpIdx]
			}
		}

		if imageRef == nil {
			foundImageRef, err := d.imageProvider.GetImage(ctx, imageDef)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get image for a node")
			}

			imageRef = foundImageRef
		}

		nodeGrpImages[nodeGrpIdx] = imageRef
	}

	return nodeGrpImages, nil
}

func (d *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	clusterID := uuid.NewString()

	d.logger.Info("gathering node images")

	nodeGrpImages, err := d.getImagesForNodeGrps(ctx, def.NodeGroups)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch images")
	}

	d.logger.Info("deploying nodes")

	nodes := make([]*NodeInfo, 0)
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

	var nodeOpts []*DeployNodeOptions
	var nodeNodeGrps []*clusterdef.NodeGroup
	for nodeGrpIdx, nodeGrp := range def.NodeGroups {
		// We grab the number of nodes to allocate and copy the group out
		// for each individual node with a count of 1
		numNodes := nodeGrp.Count
		nodeGrp := to.Ptr(*nodeGrp)
		nodeGrp.Count = 1

		for grpNodeIdx := 0; grpNodeIdx < numNodes; grpNodeIdx++ {
			d.logger.Info("deploying", zap.Any("nodeGrp", nodeGrp))

			image := nodeGrpImages[nodeGrpIdx]

			deployOpts := &DeployNodeOptions{
				Purpose:            def.Purpose,
				ClusterID:          clusterID,
				Image:              image,
				ImageServerVersion: nodeGrp.Version,
				Expiry:             def.Expiry,
				EnvVars:            nodeGrp.Docker.EnvVars,
			}

			nodeOpts = append(nodeOpts, deployOpts)
			nodeNodeGrps = append(nodeNodeGrps, nodeGrp)
		}
	}

	waitCh := make(chan error)
	for _, deployOpts := range nodeOpts {
		go func(deployOpts *DeployNodeOptions) {
			d.logger.Info("deploying node", zap.Any("deployOpts", deployOpts))

			node, err := d.controller.DeployNode(ctx, deployOpts)
			if err != nil {
				waitCh <- errors.Wrap(err, "failed to deploy a node")
				return
			}

			d.logger.Info("deployed node",
				zap.String("address", node.IPAddress),
				zap.String("id", node.NodeID),
				zap.String("container", node.ContainerID))

			nodes = append(nodes, node)
			waitCh <- nil
		}(deployOpts)
	}
	for range nodeOpts {
		err := <-waitCh
		if err != nil {
			return nil, err
		}
	}

	d.logger.Info("nodes deployed", zap.String("cluster", clusterID))

	// we cheat for now...
	clusters, err := d.listClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}

	var thisCluster *ClusterInfo
	for _, cluster := range clusters {
		if cluster.ClusterID == clusterID {
			thisCluster = cluster
		}
	}
	if thisCluster == nil {
		return nil, errors.New("failed to find new cluster after deployment")
	}

	leaveNodesAfterReturn = true

	// we need to sort the nodes by server version so that the oldest server version
	// is the first one initialized, otherwise in mixed-version clusters, we might
	// end up initializing the higher version nodes first, disallowing older nodes
	// from being initialized into the cluster (couchbase does not permit downgrades).
	slices.SortFunc(nodes, func(a, b *NodeInfo) bool {
		return semver.Compare("v"+a.InitialServerVersion, "v"+b.InitialServerVersion) < 0
	})
	d.logger.Debug("reordered setup order", zap.Any("nodes", nodes))

	var setupNodeOpts []*clustercontrol.SetupNewClusterNodeOptions
	for nodeIdx, node := range nodes {
		nodeGrp := nodeNodeGrps[nodeIdx]

		services := nodeGrp.Services
		if len(services) == 0 {
			services = []clusterdef.Service{
				clusterdef.KvService,
				clusterdef.IndexService,
				clusterdef.QueryService,
				clusterdef.SearchService,
			}
		}

		nsServices, err := clusterdef.ServicesToNsServices(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

		setupNodeOpts = append(setupNodeOpts, &clustercontrol.SetupNewClusterNodeOptions{
			Address:  node.IPAddress,
			Services: nsServices,
		})
	}

	var clusterServices []clusterdef.Service
	for _, node := range setupNodeOpts {
		for _, serviceName := range node.Services {
			service := clusterdef.Service(serviceName)
			if !slices.Contains(clusterServices, service) {
				clusterServices = append(clusterServices, service)
			}
		}
	}

	kvMemoryQuotaMB := 256
	indexMemoryQuotaMB := 256
	ftsMemoryQuotaMB := 256
	cbasMemoryQuotaMB := 1024
	eventingMemoryQuotaMB := 256
	username := "Administrator"
	password := "password"

	hasKvService := slices.Contains(clusterServices, clusterdef.KvService)
	hasIndexService := slices.Contains(clusterServices, clusterdef.IndexService)
	hasFtsService := slices.Contains(clusterServices, clusterdef.SearchService)
	hasAnalyticsService := slices.Contains(clusterServices, clusterdef.AnalyticsService)
	hasEventingService := slices.Contains(clusterServices, clusterdef.EventingService)

	if !hasKvService {
		kvMemoryQuotaMB = 0
	}
	if !hasIndexService {
		indexMemoryQuotaMB = 0
	}
	if !hasFtsService {
		ftsMemoryQuotaMB = 0
	}
	if !hasAnalyticsService {
		cbasMemoryQuotaMB = 0
	}
	if !hasEventingService {
		eventingMemoryQuotaMB = 0
	}

	if def.Docker.KvMemoryMB > 0 {
		kvMemoryQuotaMB = def.Docker.KvMemoryMB
	}
	if def.Docker.IndexMemoryMB > 0 {
		indexMemoryQuotaMB = def.Docker.IndexMemoryMB
	}
	if def.Docker.FtsMemoryMB > 0 {
		ftsMemoryQuotaMB = def.Docker.FtsMemoryMB
	}
	if def.Docker.CbasMemoryMB > 0 {
		cbasMemoryQuotaMB = def.Docker.CbasMemoryMB
	}
	if def.Docker.EventingMemoryMB > 0 {
		eventingMemoryQuotaMB = def.Docker.EventingMemoryMB
	}
	if def.Docker.Username != "" {
		username = def.Docker.Username
	}
	if def.Docker.Password != "" {
		password = def.Docker.Password
	}

	if kvMemoryQuotaMB < 256 && hasKvService {
		d.logger.Warn("kv memory must be at least 256, adjusting it...")
		kvMemoryQuotaMB = 256
	}
	if indexMemoryQuotaMB < 256 && hasIndexService {
		d.logger.Warn("index memory must be at least 256, adjusting it...")
		indexMemoryQuotaMB = 256
	}
	if ftsMemoryQuotaMB < 256 && hasFtsService {
		d.logger.Warn("fts memory must be at least 256, adjusting it...")
		ftsMemoryQuotaMB = 256
	}
	if cbasMemoryQuotaMB < 1024 && hasAnalyticsService {
		d.logger.Warn("cbas memory must be at least 1024, adjusting it...")
		cbasMemoryQuotaMB = 1024
	}
	if eventingMemoryQuotaMB < 256 && hasEventingService {
		d.logger.Warn("eventing memory must be at least 256, adjusting it...")
		eventingMemoryQuotaMB = 256
	}

	setupOpts := &clustercontrol.SetupNewClusterOptions{
		KvMemoryQuotaMB:       kvMemoryQuotaMB,
		IndexMemoryQuotaMB:    indexMemoryQuotaMB,
		FtsMemoryQuotaMB:      ftsMemoryQuotaMB,
		CbasMemoryQuotaMB:     cbasMemoryQuotaMB,
		EventingMemoryQuotaMB: eventingMemoryQuotaMB,
		Username:              username,
		Password:              password,
		Nodes:                 setupNodeOpts,
	}

	clusterMgr := clustercontrol.ClusterManager{
		Logger: d.logger,
	}
	err = clusterMgr.SetupNewCluster(ctx, setupOpts)
	if err != nil {
		return nil, errors.Wrap(err, "failed to setup cluster")
	}

	return thisCluster, nil
}

type deployedNodeInfo struct {
	ContainerID string
	IPAddress   string
	OTPNode     string
	Version     string
	Services    []clusterdef.Service
}

type deployedClusterInfo struct {
	ID      string
	Purpose string
	Expiry  time.Time
	Nodes   []*deployedNodeInfo
}

func (d *Deployer) getClusterInfo(ctx context.Context, clusterID string) (*deployedClusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var purpose string
	var expiry time.Time
	var nodeInfo []*deployedNodeInfo

	for _, node := range nodes {
		if node.ClusterID == clusterID {
			if node.Purpose != "" {
				purpose = node.Purpose
			}
			if !node.Expiry.IsZero() && node.Expiry.After(expiry) {
				expiry = node.Expiry
			}

			nodeCtrl := clustercontrol.NodeManager{
				Endpoint: fmt.Sprintf("http://%s:8091", node.IPAddress),
			}
			thisNodeInfo, err := nodeCtrl.Controller().GetLocalInfo(ctx)
			if err != nil {
				return nil, errors.Wrap(err, "failed to list a nodes services")
			}

			services, err := clusterdef.NsServicesToServices(thisNodeInfo.Services)
			if err != nil {
				return nil, errors.Wrap(err, "failed to generate services list")
			}

			nodeInfo = append(nodeInfo, &deployedNodeInfo{
				ContainerID: node.ContainerID,
				IPAddress:   node.IPAddress,
				OTPNode:     thisNodeInfo.OTPNode,
				Version:     node.InitialServerVersion,
				Services:    services,
			})
		}
	}

	return &deployedClusterInfo{
		ID:      clusterID,
		Purpose: purpose,
		Expiry:  expiry,
		Nodes:   nodeInfo,
	}, nil
}

func (d *Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	var nodeGroups []*clusterdef.NodeGroup

	for _, node := range clusterInfo.Nodes {
		nodeGroups = append(nodeGroups, &clusterdef.NodeGroup{
			Count:    1,
			Version:  node.Version,
			Services: node.Services,
		})
	}

	return &clusterdef.Cluster{
		Purpose:    clusterInfo.Purpose,
		NodeGroups: nodeGroups,
	}, nil
}

func (d *Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return errors.New("cannot modify a cluster with no nodes")
	}

	for _, node := range clusterInfo.Nodes {
		err := d.controller.UpdateExpiry(ctx, node.ContainerID, newExpiryTime)
		if err != nil {
			return errors.Wrap(err, "failed to update node expiry")
		}
	}

	return nil
}

func (d *Deployer) addRemoveNodes(
	ctx context.Context,
	clusterInfo *deployedClusterInfo,
	nodesToAdd []*clusterdef.NodeGroup,
	nodesToRemove []*deployedNodeInfo,
) ([]string, error) {
	if len(nodesToRemove) == 0 && len(nodesToAdd) == 0 {
		return nil, nil
	}

	ctrlNode := clusterInfo.Nodes[0]

	d.logger.Debug("selected node for initial add commands",
		zap.String("address", ctrlNode.IPAddress))

	nodeCtrl := clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", ctrlNode.IPAddress),
	}

	d.logger.Info("gathering node images")

	nodesToAddImages, err := d.getImagesForNodeGrps(ctx, nodesToAdd)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch images")
	}

	d.logger.Info("deploying new node containers")

	var deployedNodeIds []string
	var setupNodeOpts []*clustercontrol.AddNodeOptions
	for nodeGrpIdx, nodeGrp := range nodesToAdd {
		image := nodesToAddImages[nodeGrpIdx]

		deployOpts := &DeployNodeOptions{
			Purpose:            clusterInfo.Purpose,
			ClusterID:          clusterInfo.ID,
			Image:              image,
			ImageServerVersion: nodeGrp.Version,
			Expiry:             time.Until(clusterInfo.Expiry),
			EnvVars:            nodeGrp.Docker.EnvVars,
		}

		d.logger.Info("deploying node", zap.Any("deployOpts", deployOpts))

		node, err := d.controller.DeployNode(ctx, deployOpts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to deploy a node")
		}

		nsServices, err := clusterdef.ServicesToNsServices(nodeGrp.Services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

		setupNodeOpts = append(setupNodeOpts, &clustercontrol.AddNodeOptions{
			ServerGroup: "0",
			Address:     node.IPAddress,
			Services:    nsServices,
			Username:    "",
			Password:    "",
		})

		deployedNodeIds = append(deployedNodeIds, node.NodeID)
	}

	d.logger.Info("registering new nodes")

	for _, addNodeOpts := range setupNodeOpts {
		err := nodeCtrl.Controller().AddNode(ctx, addNodeOpts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to register new node")
		}
	}

	otpsToRemove := make([]string, len(nodesToRemove))
	for nodeIdx, nodeToRemove := range nodesToRemove {
		otpsToRemove[nodeIdx] = nodeToRemove.OTPNode
	}

	// once all the new nodes are registered, we re-select a node to work with that is
	// not being removed from the cluster, which can now include the new nodes...

	for _, clusterNode := range clusterInfo.Nodes {
		if !slices.Contains(otpsToRemove, clusterNode.OTPNode) {
			ctrlNode = clusterNode
		}
	}

	d.logger.Debug("selected node for remove and rebalance commands",
		zap.String("address", ctrlNode.IPAddress))

	nodeCtrl = clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", ctrlNode.IPAddress),
	}

	d.logger.Info("initiating rebalance")

	err = nodeCtrl.Rebalance(ctx, otpsToRemove)
	if err != nil {
		return nil, errors.Wrap(err, "failed to start rebalance")
	}

	d.logger.Info("waiting for rebalance completion")

	err = nodeCtrl.WaitForNoRunningTasks(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for tasks to complete")
	}

	for _, node := range nodesToRemove {
		d.logger.Info("removing node",
			zap.String("container", node.ContainerID))

		d.controller.RemoveNode(ctx, node.ContainerID)
	}

	return deployedNodeIds, nil
}

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return errors.New("cannot modify a cluster with no nodes")
	}

	if len(def.NodeGroups) > 0 {
		nodesToRemove := clusterInfo.Nodes
		nodesToAdd := []*clusterdef.NodeGroup{}

		// build the list of individualized nodes we need
		for _, nodeGrp := range def.NodeGroups {
			numNodes := nodeGrp.Count
			nodeGrp := to.Ptr(*nodeGrp)
			nodeGrp.Count = 1

			for grpNodeIdx := 0; grpNodeIdx < numNodes; grpNodeIdx++ {
				nodesToAdd = append(nodesToAdd, nodeGrp)
			}
		}

		// first iterate and find any exact matches and use those
		nodesToAdd = slices.DeleteFunc(nodesToAdd, func(nodeGrp *clusterdef.NodeGroup) bool {
			if nodeGrp.ForceNew {
				return false
			}

			for nodeIdx, node := range nodesToRemove {
				if node.Version != nodeGrp.Version {
					continue
				}

				serviceCmp := clusterdef.CompareServices(node.Services, nodeGrp.Services)
				if serviceCmp != 0 {
					continue
				}

				nodesToRemove = slices.Delete(nodesToRemove, nodeIdx, nodeIdx+1)
				return true
			}

			return false
		})

		d.logger.Debug("identified nodes to add",
			zap.Any("nodes", nodesToAdd))
		d.logger.Debug("identified nodes to remove",
			zap.Any("nodes", nodesToRemove))

		_, err := d.addRemoveNodes(ctx, clusterInfo, nodesToAdd, nodesToRemove)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Deployer) AddNode(ctx context.Context, clusterID string) (string, error) {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return "", errors.New("cannot add a node to a cluster with no nodes")
	}

	nodeVersion := clusterInfo.Nodes[0].Version
	nodeServices := clusterInfo.Nodes[0].Services

	for _, node := range clusterInfo.Nodes {
		if nodeVersion != node.Version || slices.Compare(nodeServices, node.Services) != 0 {
			return "", errors.New("cluster must have homogenous versions to add a node")
		}
	}

	nodeIds, err := d.addRemoveNodes(ctx, clusterInfo, []*clusterdef.NodeGroup{
		{
			Count:    1,
			Version:  nodeVersion,
			Services: nodeServices,
		},
	}, nil)
	if err != nil {
		return "", err
	}

	if len(nodeIds) != 1 {
		return "", errors.New("unexpected number of node ids returned")
	}

	return nodeIds[0], nil
}

func (d *Deployer) RemoveNode(ctx context.Context, clusterID string, nodeID string) error {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	node, err := d.getNode(ctx, clusterID, nodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get node")
	}

	// we find the node the user selected, and a secondary node that we
	// can use to actually manipulate the cluster
	var foundNode *deployedNodeInfo
	for _, clusterNode := range clusterInfo.Nodes {
		if clusterNode.ContainerID == node.ContainerID {
			foundNode = clusterNode
		}
	}
	if foundNode == nil {
		return errors.Wrap(err, "failed to find deployed node")
	}

	_, err = d.addRemoveNodes(ctx, clusterInfo, nil, []*deployedNodeInfo{
		foundNode,
	})
	if err != nil {
		return err
	}

	return nil
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

func (d *Deployer) getCluster(ctx context.Context, clusterID string) (*ClusterInfo, error) {
	clusters, err := d.listClusters(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var thisCluster *ClusterInfo
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

func (d *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	thisCluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	var connstrAddrs []string
	var connstrTlsAddrs []string
	var mgmtAddr string
	var mgmtTlsAddr string
	for _, node := range thisCluster.Nodes {
		kvPort := 11210
		kvTlsPort := 11207
		mgmtPort := 8091
		mgmtTlsPort := 18091

		if kvPort == 11210 {
			connstrAddrs = append(connstrAddrs, node.IPAddress)
		} else {
			connstrAddrs = append(connstrAddrs, fmt.Sprintf("%s:%d", node.IPAddress, kvPort))
		}

		if kvTlsPort == 11207 {
			connstrTlsAddrs = append(connstrTlsAddrs, node.IPAddress)
		} else {
			connstrTlsAddrs = append(connstrTlsAddrs, fmt.Sprintf("%s:%d", node.IPAddress, kvTlsPort))
		}

		mgmtAddr = fmt.Sprintf("%s:%d", node.IPAddress, mgmtPort)
		mgmtTlsAddr = fmt.Sprintf("%s:%d", node.IPAddress, mgmtTlsPort)
	}

	connStr := fmt.Sprintf("couchbase://%s", strings.Join(connstrAddrs, ","))
	connStrTls := fmt.Sprintf("couchbases://%s", strings.Join(connstrTlsAddrs, ","))
	mgmt := fmt.Sprintf("http://%s", mgmtAddr)
	mgmtTls := fmt.Sprintf("https://%s", mgmtTlsAddr)

	return &deployment.ConnectInfo{
		ConnStr:    connStr,
		ConnStrTls: connStrTls,
		Mgmt:       mgmt,
		MgmtTls:    mgmtTls,
	}, nil
}

func (d *Deployer) Cleanup(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	curTime := time.Now()
	for _, node := range nodes {
		if !node.Expiry.IsZero() && !node.Expiry.After(curTime) {
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

func (d *Deployer) getController(ctx context.Context, clusterID string) (*clustercontrol.NodeManager, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	nodeCtrl := &clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", clusterInfo.Nodes[0].IPAddress),
	}

	return nodeCtrl, nil
}

func (d *Deployer) getAgent(ctx context.Context, clusterID string, bucketName string) (*gocbcorex.Agent, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	httpEndpoint := fmt.Sprintf("%s:8091", clusterInfo.Nodes[0].IPAddress)
	memdEndpoint := fmt.Sprintf("%s:11210", clusterInfo.Nodes[0].IPAddress)

	agent, err := gocbcorex.CreateAgent(ctx, gocbcorex.AgentOptions{
		Logger:     d.logger.Named("agent"),
		TLSConfig:  nil,
		BucketName: bucketName,
		Authenticator: &gocbcorex.PasswordAuthenticator{
			Username: "Administrator",
			Password: "password",
		},
		SeedConfig: gocbcorex.SeedConfig{
			HTTPAddrs: []string{httpEndpoint},
			MemdAddrs: []string{memdEndpoint},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create gocbcorex agent")
	}

	return agent, nil
}

func (d *Deployer) ListUsers(ctx context.Context, clusterID string) ([]deployment.UserInfo, error) {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster controller")
	}

	resp, err := controller.Controller().ListUsers(ctx, &clustercontrol.ListUsersRequest{
		Order:    "asc",
		PageSize: 100,
		SortBy:   "id",
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list users")
	}

	var users []deployment.UserInfo
	for _, user := range resp.Users {
		canRead := false
		canWrite := false
		for _, perm := range user.Roles {
			if perm.Role == "admin" {
				canWrite = true
				canRead = true
			} else if perm.Role == "data_reader" {
				canRead = true
			}
		}

		users = append(users, deployment.UserInfo{
			Username: user.ID,
			CanRead:  canRead,
			CanWrite: canWrite,
		})
	}

	return users, nil
}

func (d *Deployer) CreateUser(ctx context.Context, clusterID string, opts *deployment.CreateUserOptions) error {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster controller")
	}

	var roles []string
	if opts.CanWrite {
		roles = append(roles, "admin")
	} else if opts.CanRead {
		roles = append(roles,
			"ro_admin",
			"analytics_reader",
			"data_reader[*]",
			"views_reader[*]",
			"query_select[*]",
			"fts_searcher[*]")
	}

	err = controller.Controller().CreateUser(ctx, opts.Username, &clustercontrol.CreateUserRequest{
		Name:     "",
		Password: opts.Password,
		Roles:    roles,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create user")
	}

	return nil
}

func (d *Deployer) DeleteUser(ctx context.Context, clusterID string, username string) error {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster controller")
	}

	err = controller.Controller().DeleteUser(ctx, username)
	if err != nil {
		return errors.Wrap(err, "failed to delete user")
	}

	return nil
}

func (d *Deployer) ListBuckets(ctx context.Context, clusterID string) ([]deployment.BucketInfo, error) {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster controller")
	}

	resp, err := controller.Controller().ListBuckets(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list buckets")
	}

	var buckets []deployment.BucketInfo
	for _, bucket := range resp {
		buckets = append(buckets, deployment.BucketInfo{
			Name: bucket.Name,
		})
	}

	return buckets, nil
}

func (d *Deployer) LoadSampleBucket(ctx context.Context, clusterID string, bucketName string) error {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster controller")
	}

	err = controller.Controller().LoadSampleBucket(ctx, bucketName)
	if err != nil {
		return errors.Wrap(err, "failed to load sample bucket")
	}

	err = controller.WaitForNoRunningTasks(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to wait for tasks to complete after loading sample bucket")
	}

	return nil
}

func (d *Deployer) CreateBucket(ctx context.Context, clusterID string, opts *deployment.CreateBucketOptions) error {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster controller")
	}

	ramQuotaMb := 256
	if opts.RamQuotaMB > 0 {
		ramQuotaMb = opts.RamQuotaMB
	}

	numReplicas := 1
	if opts.NumReplicas > 1 {
		numReplicas = opts.NumReplicas
	}

	err = controller.Controller().CreateBucket(ctx, &clustercontrol.CreateBucketRequest{
		Name:                   opts.Name,
		BucketType:             "membase",
		StorageBackend:         "couchstore",
		AutoCompactionDefined:  false,
		EvictionPolicy:         "valueOnly",
		ThreadsNumber:          3,
		ReplicaNumber:          numReplicas,
		DurabilityMinLevel:     "none",
		CompressionMode:        "passive",
		MaxTTL:                 0,
		ReplicaIndex:           0,
		ConflictResolutionType: "seqno",
		RamQuotaMB:             ramQuotaMb,
		FlushEnabled:           opts.FlushEnabled,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create bucket")
	}

	return nil
}

func (d *Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster controller")
	}

	err = controller.Controller().DeleteBucket(ctx, bucketName)
	if err != nil {
		return errors.Wrap(err, "failed to delete bucket")
	}

	return nil
}

func (d *Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster controller")
	}

	resp, err := controller.Controller().GetTrustedCAs(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to get trusted CAs")
	}

	lastCert := (*resp)[len(*resp)-1]

	return strings.TrimSpace(lastCert.Pem), nil
}

func (d *Deployer) GetGatewayCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("dockerdeploy does not support getting gateway certificates")
}

func (d *Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error) {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	results, err := agent.Query(ctx, &gocbcorex.QueryOptions{
		Statement: query,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to execute query")
	}

	rows := make([]json.RawMessage, 0)
	for results.HasMoreRows() {
		row, err := results.ReadRow()
		if err != nil {
			return "", errors.Wrap(err, "failed to read row")
		}

		rows = append(rows, row)
	}

	rowsBytes, err := json.Marshal(rows)
	if err != nil {
		return "", errors.Wrap(err, "failed to serialize rows")
	}

	return string(rowsBytes), nil
}

func (d *Deployer) ListCollections(ctx context.Context, clusterID string, bucketName string) ([]deployment.ScopeInfo, error) {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	manifest, err := agent.GetCollectionManifest(ctx, &cbmgmtx.GetCollectionManifestOptions{
		BucketName: bucketName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch collection manifest")
	}

	var scopes []deployment.ScopeInfo
	for _, scope := range manifest.Scopes {
		var collections []deployment.CollectionInfo
		for _, collection := range scope.Collections {
			collections = append(collections, deployment.CollectionInfo{
				Name: collection.Name,
			})
		}
		scopes = append(scopes, deployment.ScopeInfo{
			Name:        scope.Name,
			Collections: collections,
		})
	}

	return scopes, nil
}

func (d *Deployer) CreateScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	_, err = agent.CreateScope(ctx, &cbmgmtx.CreateScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create scope")
	}

	return nil
}

func (d *Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	_, err = agent.CreateCollection(ctx, &cbmgmtx.CreateCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create collection")
	}

	return nil
}

func (d *Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	_, err = agent.DeleteScope(ctx, &cbmgmtx.DeleteScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete scope")
	}

	return nil
}

func (d *Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	_, err = agent.DeleteCollection(ctx, &cbmgmtx.DeleteCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete collection")
	}

	return nil
}

func (d *Deployer) getNode(ctx context.Context, clusterID, nodeID string) (*NodeInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var foundNode *NodeInfo
	for _, node := range nodes {
		if node.ClusterID == clusterID && node.NodeID == nodeID {
			foundNode = node
		}
	}
	if foundNode == nil {
		return nil, fmt.Errorf("failed to find node with id `%s`", nodeID)
	}

	return foundNode, nil
}

func (d *Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeID string, blockType deployment.BlockNodeTrafficType) error {
	node, err := d.getNode(ctx, clusterID, nodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get node")
	}

	var tcType TrafficControlType
	switch blockType {
	case deployment.BlockNodeTrafficNodes:
		tcType = TrafficControlBlockNodes
	case deployment.BlockNodeTrafficClients:
		tcType = TrafficControlBlockClients
	case deployment.BlockNodeTrafficAll:
		tcType = TrafficControlBlockAll
	}
	err = d.controller.SetTrafficControl(ctx, node.ContainerID, tcType)
	if err != nil {
		return errors.Wrap(err, "failed to block traffic")
	}

	return nil
}

func (d *Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeID string) error {
	node, err := d.getNode(ctx, clusterID, nodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get node")
	}

	err = d.controller.SetTrafficControl(ctx, node.ContainerID, TrafficControlAllowAll)
	if err != nil {
		return errors.Wrap(err, "failed to allow traffic")
	}

	return nil
}

func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return nil, errors.New("cannot collection logs from a cluster with no nodes")
	}

	nodeCtrl := clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", clusterInfo.Nodes[0].IPAddress),
	}

	nodeOtps, err := nodeCtrl.Controller().ListNodeOTPs(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	d.logger.Info("beginning log collection", zap.Strings("nodes", nodeOtps))

	err = nodeCtrl.Controller().BeginLogsCollection(ctx, &clustercontrol.BeginLogsCollectionOptions{
		Nodes:             nodeOtps,
		LogRedactionLevel: "none",
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to begin log collection")
	}

	d.logger.Info("waiting for log collection to start")

	err = nodeCtrl.WaitForTaskRunning(ctx, "clusterLogsCollection")
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for log collection to start")
	}

	d.logger.Info("waiting for log collection to complete (this can take a _long_ time)")

	logPaths, err := nodeCtrl.WaitForLogCollection(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to wait for log collection to complete")
	}

	nodeInfoFromIp := func(ipAddress string) *deployedNodeInfo {
		for _, nodeInfo := range clusterInfo.Nodes {
			if nodeInfo.IPAddress == ipAddress {
				return nodeInfo
			}
		}
		return nil
	}

	var destPaths []string
	for nodeId, filePath := range logPaths {
		otpParts := strings.Split(nodeId, "@")
		if len(otpParts) != 2 {
			return nil, errors.New("unexpected node otp format")
		}
		ipAddress := otpParts[1]

		nodeInfo := nodeInfoFromIp(ipAddress)
		if nodeInfo == nil {
			return nil, fmt.Errorf("failed to find node for ip %s", ipAddress)
		}
		containerId := nodeInfo.ContainerID

		fileName := path.Base(filePath)
		destFilePath := path.Join(destPath, fileName)

		if !d.logger.Level().Enabled(zapcore.DebugLevel) {
			d.logger.Info("downloading log from node",
				zap.String("node", nodeId))
		} else {
			d.logger.Info("downloading log from node",
				zap.String("node", nodeId),
				zap.String("ipAddress", ipAddress),
				zap.String("container", containerId),
				zap.String("srcPath", filePath),
				zap.String("destPath", destFilePath))
		}

		resp, _, err := d.dockerCli.CopyFromContainer(ctx, containerId, filePath)
		if err != nil {
			return nil, errors.Wrap(err, "failed to copy from container")
		}
		defer resp.Close()

		tarRdr := tar.NewReader(resp)
		_, err = tarRdr.Next()
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse transmitted file")
		}

		fileWrt, err := os.Create(destFilePath)
		if err != nil {
			return nil, errors.Wrap(err, "failed to open destination file for writing")
		}

		_, err = io.Copy(fileWrt, tarRdr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to copy container file to local disk")
		}

		destPaths = append(destPaths, destFilePath)
	}

	return destPaths, nil
}

func (d *Deployer) ListImages(ctx context.Context) ([]deployment.Image, error) {
	return d.imageProvider.ListImages(ctx)
}

func (d *Deployer) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	return d.imageProvider.SearchImages(ctx, version)
}

func (d *Deployer) PauseNode(ctx context.Context, clusterID string, nodeID string) error {
	node, err := d.getNode(ctx, clusterID, nodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get node")
	}

	err = d.dockerCli.ContainerPause(ctx, node.ContainerID)
	if err != nil {
		return errors.Wrap(err, "failed to pause container")
	}

	return nil
}

func (d *Deployer) UnpauseNode(ctx context.Context, clusterID string, nodeID string) error {
	node, err := d.getNode(ctx, clusterID, nodeID)
	if err != nil {
		return errors.Wrap(err, "failed to get node")
	}

	err = d.dockerCli.ContainerUnpause(ctx, node.ContainerID)
	if err != nil {
		return errors.Wrap(err, "failed to unpause container")
	}

	return nil
}
