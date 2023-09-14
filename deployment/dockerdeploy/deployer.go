package dockerdeploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
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
		if node.Expiry.After(cluster.Expiry) {
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

			d.logger.Info("deployed node")

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

	d.logger.Info("nodes deployed")

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

	kvMemoryQuotaMB := 256
	indexMemoryQuotaMB := 256
	ftsMemoryQuotaMB := 256
	cbasMemoryQuotaMB := 1024
	eventingMemoryQuotaMB := 256
	username := "Administrator"
	password := "password"
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

	if kvMemoryQuotaMB < 256 {
		d.logger.Warn("kv memory must be at least 256, adjusting it...")
		kvMemoryQuotaMB = 256
	}
	if indexMemoryQuotaMB < 256 {
		d.logger.Warn("index memory must be at least 256, adjusting it...")
		indexMemoryQuotaMB = 256
	}
	if ftsMemoryQuotaMB < 256 {
		d.logger.Warn("fts memory must be at least 256, adjusting it...")
		ftsMemoryQuotaMB = 256
	}
	if cbasMemoryQuotaMB < 1024 {
		d.logger.Warn("cbas memory must be at least 1024, adjusting it...")
		cbasMemoryQuotaMB = 1024
	}
	if eventingMemoryQuotaMB < 256 {
		d.logger.Warn("eventing memory must be at least 256, adjusting it...")
		eventingMemoryQuotaMB = 256
	}

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
	Purpose string
	Nodes   []*deployedNodeInfo
}

func (d *Deployer) getClusterInfo(ctx context.Context, clusterID string) (*deployedClusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var purpose string
	var nodeInfo []*deployedNodeInfo

	for _, node := range nodes {
		if node.ClusterID == clusterID {
			if node.Purpose != "" {
				purpose = node.Purpose
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
		Purpose: purpose,
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

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return errors.New("cannot modify a cluster with no nodes")
	}

	nodeCtrl := clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", clusterInfo.Nodes[0].IPAddress),
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

		d.logger.Info("gathering node images")

		nodesToAddImages, err := d.getImagesForNodeGrps(ctx, nodesToAdd)
		if err != nil {
			return errors.Wrap(err, "failed to fetch images")
		}

		d.logger.Info("deploying new node containers")

		var setupNodeOpts []*clustercontrol.AddNodeOptions
		for nodeGrpIdx, nodeGrp := range nodesToAdd {
			image := nodesToAddImages[nodeGrpIdx]

			deployOpts := &DeployNodeOptions{
				Purpose:            def.Purpose,
				ClusterID:          clusterID,
				Image:              image,
				ImageServerVersion: nodeGrp.Version,
				Expiry:             def.Expiry,
			}

			d.logger.Info("deploying node", zap.Any("deployOpts", deployOpts))

			node, err := d.controller.DeployNode(ctx, deployOpts)
			if err != nil {
				return errors.Wrap(err, "failed to deploy a node")
			}

			nsServices, err := clusterdef.ServicesToNsServices(nodeGrp.Services)
			if err != nil {
				return errors.Wrap(err, "failed to generate ns server services list")
			}

			setupNodeOpts = append(setupNodeOpts, &clustercontrol.AddNodeOptions{
				ServerGroup: "0",
				Address:     node.IPAddress,
				Services:    nsServices,
				Username:    "",
				Password:    "",
			})
		}

		d.logger.Info("registering new nodes")

		for _, addNodeOpts := range setupNodeOpts {
			err := nodeCtrl.Controller().AddNode(ctx, addNodeOpts)
			if err != nil {
				return errors.Wrap(err, "failed to register new node")
			}
		}

		d.logger.Info("initiating rebalance")

		otpsToRemove := make([]string, len(nodesToRemove))
		for nodeIdx, nodeToRemove := range nodesToRemove {
			otpsToRemove[nodeIdx] = nodeToRemove.OTPNode
		}

		err = nodeCtrl.Rebalance(ctx, otpsToRemove)
		if err != nil {
			return errors.Wrap(err, "failed to start rebalance")
		}

		d.logger.Info("waiting for rebalance completion")

		err = nodeCtrl.WaitForNoRunningTasks(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to wait for tasks to complete")
		}

		for _, node := range nodesToRemove {
			d.logger.Info("removing node",
				zap.String("container", node.ContainerID))

			d.controller.RemoveNode(ctx, node.ContainerID)
		}
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

func (d *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
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

	var connstrAddrs []string
	var mgmtAddr string
	for _, node := range thisCluster.Nodes {
		kvPort := 11210
		mgmtPort := 8091

		if kvPort == 11210 {
			connstrAddrs = append(connstrAddrs, node.IPAddress)
		} else {
			connstrAddrs = append(connstrAddrs, fmt.Sprintf("%s:%d", node.IPAddress, 11210))
		}

		mgmtAddr = fmt.Sprintf("%s:%d", node.IPAddress, mgmtPort)
	}

	connStr := fmt.Sprintf("couchbase://%s", strings.Join(connstrAddrs, ","))
	mgmt := fmt.Sprintf("http://%s", mgmtAddr)

	return &deployment.ConnectInfo{
		ConnStr: connStr,
		Mgmt:    mgmt,
	}, nil
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
