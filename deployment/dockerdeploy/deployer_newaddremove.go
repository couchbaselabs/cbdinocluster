package dockerdeploy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"
)

func (d *Deployer) getImagesForNodeGrps(ctx context.Context, nodeGrps []*clusterdef.NodeGroup, isColumnar bool) ([]*ImageRef, error) {
	nodeGrpDefs := make([]*ImageDef, len(nodeGrps))
	nodeGrpImages := make([]*ImageRef, len(nodeGrps))
	for nodeGrpIdx, nodeGrp := range nodeGrps {
		if nodeGrp.Docker.Image != "" {
			foundImageRef, err := d.imageProvider.GetImageRaw(ctx, nodeGrp.Docker.Image)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get image for a node")
			}

			nodeGrpImages[nodeGrpIdx] = foundImageRef
			continue
		}

		versionInfo, err := versionident.Identify(ctx, nodeGrp.Version)
		if err != nil {
			return nil, errors.Wrap(err, "failed to identify version")
		}

		imageDef := &ImageDef{
			Version:             versionInfo.Version,
			BuildNo:             versionInfo.BuildNo,
			UseCommunityEdition: versionInfo.CommunityEdition,
			UseServerless:       versionInfo.Serverless,
			UseColumnar:         isColumnar,
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

func (d *Deployer) newCluster(ctx context.Context, def *clusterdef.Cluster) (*clusterInfo, error) {
	if def.Columnar {
		for _, nodeGrp := range def.NodeGroups {
			if len(nodeGrp.Services) != 0 {
				return nil, errors.New("columnar clusters cannot specify services")
			}

			nodeGrp.Services = []clusterdef.Service{
				clusterdef.KvService,
				clusterdef.AnalyticsService,
			}
		}
	}

	clusterID := uuid.NewString()

	useDns := def.Docker.EnableDNS
	if useDns && d.dnsProvider == nil {
		return nil, errors.New("cannot use dns, dns not configured")
	}

	var dnsName string
	if useDns {
		dnsHostname := d.dnsProvider.GetHostname()
		dnsName = fmt.Sprintf("%s-%s.%s",
			clusterID[:8],
			time.Now().Format("20060102"),
			dnsHostname)
	}

	var rootCaPem []byte
	var clusterCa *dinocerts.CertAuthority
	if def.Docker.UseDinoCerts {
		var err error
		clusterCa, rootCaPem, err = d.getClusterDinoCert(clusterID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get cluster dino ca")
		}
	}

	if def.Columnar {
		d.logger.Info("deploying mock s3 for blob storage")

		d.logger.Debug("deploying s3mock container")

		node, err := d.controller.DeployS3MockNode(ctx, clusterID, def.Expiry)
		if err != nil {
			return nil, errors.Wrap(err, "failed to deploy s3mock node")
		}

		d.logger.Debug("creating columnar bucket")

		bucketName := "columnar"
		req, err := http.NewRequest(
			"PUT",
			fmt.Sprintf("http://%s:9090/%s/", node.IPAddress, bucketName),
			nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create columnar s3 bucket request")
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create columnar s3 bucket")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("non-200 status code when creating columnar s3 bucket (code: %d)", resp.StatusCode)
		}

		d.logger.Info("s3 mock is ready")

		def.Docker.Analytics.BlobStorage = clusterdef.AnalyticsBlobStorageSettings{
			Region:         "local",
			Bucket:         "columnar",
			Scheme:         "s3",
			Endpoint:       fmt.Sprintf("http://%s:9090", node.IPAddress),
			AnonymousAuth:  true,
			ForcePathStyle: true,
		}
	}

	nginxContainerId := ""
	nginxIpAddress := ""
	if def.Docker.EnableLoadBalancer {
		d.logger.Info("deploying nginx for load balancing")

		node, err := d.controller.DeployNginxNode(ctx, clusterID, def.Expiry)
		if err != nil {
			return nil, errors.Wrap(err, "failed to deploy nginx node")
		}

		d.logger.Debug("nginx started", zap.String("ip", node.IPAddress))

		if clusterCa != nil {
			d.logger.Debug("uploading dinocert certificates to nginx",
				zap.String("nginx", node.NodeID))

			ip := net.ParseIP(node.IPAddress)

			var dnsNames []string
			if dnsName != "" {
				dnsNames = append(dnsNames, dnsName)
			}

			certPem, keyPem, err := clusterCa.MakeServerCertificate("nginx-"+clusterID[:8], []net.IP{ip}, dnsNames)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create nginx certificate")
			}

			var chainPem []byte
			chainPem = append(chainPem, certPem...)
			chainPem = append(chainPem, clusterCa.CertPem...)
			err = d.controller.UpdateNginxCertificates(ctx, node.ContainerID, chainPem, keyPem)
			if err != nil {
				return nil, errors.Wrap(err, "failed to upload nginx certificates")
			}
		}

		nginxContainerId = node.ContainerID
		nginxIpAddress = node.IPAddress
	}

	d.logger.Info("gathering node images")

	nodeGrpImages, err := d.getImagesForNodeGrps(ctx, def.NodeGroups, def.Columnar)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch images")
	}

	d.logger.Info("deploying nodes")

	nodes := make([]*ContainerInfo, 0)
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
				IsColumnar:         def.Columnar,
				DnsSuffix:          dnsName,
				Expiry:             def.Expiry,
				EnvVars:            nodeGrp.Docker.EnvVars,
				UseDinoCerts:       def.Docker.UseDinoCerts,
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
	thisCluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.New("failed to find new cluster after deployment")
	}

	leaveNodesAfterReturn = true

	if clusterCa != nil {
		d.logger.Info("setting up dinocert certificates", zap.String("cluster", clusterID))

		for _, node := range nodes {
			err := d.setupNodeCertificates(ctx, node, clusterCa, rootCaPem)
			if err != nil {
				return nil, errors.Wrap(err, "failed to setup node certificates")
			}
		}
	}

	if useDns {
		// since this is a net-new domain name, no need to wait for dns propagation
		err = d.updateDnsRecords(ctx, dnsName, thisCluster.Nodes, def.Columnar, nginxIpAddress, true)
		if err != nil {
			return nil, errors.Wrap(err, "failed to update dns records")
		}
	}

	if nginxContainerId != "" {
		useDinoCerts := false
		if clusterCa != nil {
			useDinoCerts = true
		}

		err = d.updateLoadBalancer(ctx, nginxContainerId, thisCluster.Nodes, def.Columnar, useDinoCerts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to update load balancer")
		}
	}

	// we need to sort the nodes by server version so that the oldest server version
	// is the first one initialized, otherwise in mixed-version clusters, we might
	// end up initializing the higher version nodes first, disallowing older nodes
	// from being initialized into the cluster (couchbase does not permit downgrades).
	// We also sort by IP address next
	slices.SortFunc(nodes, func(a, b *ContainerInfo) int {
		res := semver.Compare("v"+a.InitialServerVersion, "v"+b.InitialServerVersion)
		if res != 0 {
			return res
		}
		return strings.Compare(a.IPAddress, b.IPAddress)
	})
	d.logger.Debug("reordered setup order", zap.Any("nodes", nodes))

	var setupNodeOpts []*clustercontrol.SetupNewClusterNodeOptions
	for nodeIdx, node := range nodes {
		nodeGrp := nodeNodeGrps[nodeIdx]

		services := nodeGrp.Services
		if len(services) == 0 {
			services = DEFAULT_SERVICES
		}

		nsServices, err := clusterdef.ServicesToNsServices(services)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate ns server services list")
		}

		setupNodeOpts = append(setupNodeOpts, &clustercontrol.SetupNewClusterNodeOptions{
			Address:     node.IPAddress,
			ServerGroup: nodeGrp.ServerGroup,
			Services:    nsServices,
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

	analyticsSettings := clustercontrol.AnalyticsSettings{
		BlobStorageRegion:         def.Docker.Analytics.BlobStorage.Region,
		BlobStoragePrefix:         def.Docker.Analytics.BlobStorage.Prefix,
		BlobStorageBucket:         def.Docker.Analytics.BlobStorage.Bucket,
		BlobStorageScheme:         def.Docker.Analytics.BlobStorage.Scheme,
		BlobStorageEndpoint:       def.Docker.Analytics.BlobStorage.Endpoint,
		BlobStorageAnonymousAuth:  def.Docker.Analytics.BlobStorage.AnonymousAuth,
		BlobStorageForcePathStyle: def.Docker.Analytics.BlobStorage.ForcePathStyle,
	}
	d.logger.Debug("analytics configuration", zap.Any("settings", analyticsSettings))

	setupOpts := &clustercontrol.SetupNewClusterOptions{
		KvMemoryQuotaMB:       kvMemoryQuotaMB,
		IndexMemoryQuotaMB:    indexMemoryQuotaMB,
		FtsMemoryQuotaMB:      ftsMemoryQuotaMB,
		CbasMemoryQuotaMB:     cbasMemoryQuotaMB,
		EventingMemoryQuotaMB: eventingMemoryQuotaMB,
		Username:              username,
		Password:              password,
		Nodes:                 setupNodeOpts,
		AnalyticsSettings:     analyticsSettings,
	}

	clusterMgr := clustercontrol.ClusterManager{
		Logger: d.logger,
	}
	err = clusterMgr.SetupNewCluster(ctx, setupOpts)
	if err != nil {
		return nil, errors.Wrap(err, "failed to setup cluster")
	}

	leaveNodesAfterReturn = true
	return thisCluster, nil
}

func (d *Deployer) addRemoveNodes(
	ctx context.Context,
	clusterInfo *clusterInfoEx,
	nodesToAdd []*clusterdef.NodeGroup,
	nodesToRemove []*nodeInfoEx,
) ([]string, error) {
	if len(nodesToRemove) == 0 && len(nodesToAdd) == 0 {
		return nil, nil
	}

	ctrlNode := clusterInfo.NodesEx[0]

	d.logger.Debug("selected node for initial add commands",
		zap.String("address", ctrlNode.IPAddress))

	nodeCtrl := clustercontrol.NodeManager{
		Logger:   d.logger,
		Endpoint: fmt.Sprintf("http://%s:8091", ctrlNode.IPAddress),
	}

	d.logger.Info("gathering node images")

	nodesToAddImages, err := d.getImagesForNodeGrps(ctx, nodesToAdd, clusterInfo.IsColumnar())
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch images")
	}

	d.logger.Info("deploying new node containers")

	var deployedNodes []*ContainerInfo
	var setupNodeOpts []*clustercontrol.AddNodeOptions
	for nodeGrpIdx, nodeGrp := range nodesToAdd {
		image := nodesToAddImages[nodeGrpIdx]

		deployOpts := &DeployNodeOptions{
			Purpose:            clusterInfo.Purpose,
			ClusterID:          clusterInfo.ClusterID,
			Image:              image,
			ImageServerVersion: nodeGrp.Version,
			IsColumnar:         clusterInfo.IsColumnar(),
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
			ServerGroup: nodeGrp.ServerGroup,
			Address:     node.IPAddress,
			Services:    nsServices,
			Username:    "",
			Password:    "",
		})

		deployedNodes = append(deployedNodes, node)
	}

	if clusterInfo.UsingDinoCerts {
		d.logger.Info("setting up dinocert certificates", zap.String("cluster", clusterInfo.ClusterID))

		clusterCa, rootCaPem, err := d.getClusterDinoCert(clusterInfo.ClusterID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get cluster dino ca")
		}

		for _, node := range deployedNodes {
			err := d.setupNodeCertificates(ctx, node, clusterCa, rootCaPem)
			if err != nil {
				return nil, errors.Wrap(err, "failed to setup node certificates")
			}
		}
	}

	d.logger.Info("registering new nodes")

	for _, addNodeOpts := range setupNodeOpts {
		err := nodeCtrl.Controller().AddNode(ctx, addNodeOpts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to register new node")
		}
	}

	var nodeIpsBeingRemoved []string
	for _, node := range nodesToRemove {
		nodeIpsBeingRemoved = append(nodeIpsBeingRemoved, node.IPAddress)
	}

	var postRemovalNodes []*nodeInfo
	for _, clusterNode := range clusterInfo.Nodes {
		if !slices.Contains(nodeIpsBeingRemoved, clusterNode.IPAddress) {
			postRemovalNodes = append(postRemovalNodes, clusterNode)
		}
	}

	// only need to update if nodes were removed
	if len(nodesToRemove) > 0 {
		// only need to update if dns is enabled
		if clusterInfo.DnsName != "" {
			err = d.updateDnsRecords(ctx, clusterInfo.DnsName, postRemovalNodes, clusterInfo.IsColumnar(), clusterInfo.LoadBalancerIPAddress(), false)
			if err != nil {
				return nil, errors.Wrap(err, "failed to update dns records")
			}
		}

		for _, node := range clusterInfo.Nodes {
			if node.IsLoadBalancerNode() {
				err = d.updateLoadBalancer(ctx, node.ContainerID, postRemovalNodes, clusterInfo.IsColumnar(), clusterInfo.UsingDinoCerts)
				if err != nil {
					return nil, errors.Wrap(err, "failed to update load balancer")
				}
			}
		}
	}

	otpsToRemove := make([]string, len(nodesToRemove))
	for nodeIdx, nodeToRemove := range nodesToRemove {
		otpsToRemove[nodeIdx] = nodeToRemove.OTPNode
	}

	// once all the new nodes are registered, we re-select a node to work with that is
	// not being removed from the cluster, which can now include the new nodes...

	for _, clusterNode := range clusterInfo.NodesEx {
		if !clusterNode.IsClusterNode() {
			continue
		}

		if !slices.Contains(otpsToRemove, clusterNode.OTPNode) {
			ctrlNode = clusterNode
		}
	}

	d.logger.Debug("selected node for remove and rebalance commands",
		zap.String("address", ctrlNode.IPAddress))

	nodeCtrl = clustercontrol.NodeManager{
		Logger:   d.logger,
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

	if len(nodesToAdd) > 0 {
		thisCluster, err := d.getCluster(ctx, clusterInfo.ClusterID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get post-rebalance cluster info")
		}

		// no need to update if DNS is disabled
		if thisCluster.DnsName != "" {
			err := d.updateDnsRecords(ctx, thisCluster.DnsName, thisCluster.Nodes, thisCluster.IsColumnar(), clusterInfo.LoadBalancerIPAddress(), false)
			if err != nil {
				return nil, errors.Wrap(err, "failed to update dns records")
			}
		}

		for _, node := range clusterInfo.Nodes {
			if node.IsLoadBalancerNode() {
				err := d.updateLoadBalancer(ctx, node.ContainerID, thisCluster.Nodes, clusterInfo.IsColumnar(), clusterInfo.UsingDinoCerts)
				if err != nil {
					return nil, errors.Wrap(err, "failed to update load balancer")
				}
			}
		}
	}

	var deployedNodeIds []string
	for _, node := range deployedNodes {
		deployedNodeIds = append(deployedNodeIds, node.NodeID)
	}

	return deployedNodeIds, nil
}

func (d *Deployer) removeNodes(ctx context.Context, nodes []*ContainerInfo) error {
	var dnsToRemove []string
	for _, node := range nodes {
		dnsToRemove = d.appendNodeDnsNames(dnsToRemove, node)
	}

	waitCh := make(chan error)
	for _, node := range nodes {
		go func(node *ContainerInfo) {
			d.logger.Info("removing node",
				zap.String("id", node.NodeID),
				zap.String("container", node.ContainerID))

			d.controller.RemoveNode(ctx, node.ContainerID)

			waitCh <- nil
		}(node)
	}

	for range nodes {
		err := <-waitCh
		if err != nil {
			return err
		}
	}

	d.removeDnsNames(ctx, dnsToRemove)

	return nil
}
