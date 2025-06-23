package dockerdeploy

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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
	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/exp/slices"
	"golang.org/x/mod/semver"
)

var DEFAULT_SERVICES []clusterdef.Service = []clusterdef.Service{
	clusterdef.KvService,
	clusterdef.IndexService,
	clusterdef.QueryService,
	clusterdef.SearchService,
}

type Deployer struct {
	logger        *zap.Logger
	dockerCli     *client.Client
	imageProvider ImageProvider
	controller    *Controller
	dnsProvider   DnsProvider
}

var _ deployment.Deployer = (*Deployer)(nil)

type DeployerOptions struct {
	Logger       *zap.Logger
	DockerCli    *client.Client
	NetworkName  string
	GhcrUsername string
	GhcrPassword string
	DnsProvider  DnsProvider
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
		dnsProvider: opts.DnsProvider,
	}, nil
}

func (d *Deployer) listClusters(ctx context.Context) ([]*ClusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	// sort the nodes by their name for nicer printing later
	slices.SortFunc(nodes, func(a *NodeInfo, b *NodeInfo) int {
		return strings.Compare(a.Name, b.Name)
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
			Type:      deployment.ClusterTypeServer,
		}
		clusters = append(clusters, cluster)
		return cluster
	}

	for _, node := range nodes {
		isClusterNode := false
		if node.Type == "server-node" || node.Type == "columnar-node" {
			isClusterNode = true
		}

		cluster := getCluster(node.ClusterID)
		if isClusterNode {
			cluster.Creator = node.Creator
			cluster.Owner = node.Owner
			cluster.Purpose = node.Purpose
			cluster.DnsName = node.DnsSuffix
			if !node.Expiry.IsZero() && node.Expiry.After(cluster.Expiry) {
				cluster.Expiry = node.Expiry
			}
		}

		cluster.Nodes = append(cluster.Nodes, &ClusterNodeInfo{
			ResourceID: node.ContainerID[0:8] + "...",
			IsNode:     isClusterNode,
			NodeID:     node.NodeID,
			Name:       node.Name,
			IPAddress:  node.IPAddress,
			DnsName:    node.DnsName,
		})

		// if we hit the nginx node, set the clusters LoadBalancerIPAddress
		if node.Type == "nginx" {
			cluster.LoadBalancerIPAddress = node.IPAddress
		}

		// if any nodes are columnar nodes, the cluster is a columnar cluster
		if node.Type == "columnar-node" {
			cluster.Type = deployment.ClusterTypeColumnar
		}
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

func (d *Deployer) getClusterDinoCert(clusterID string) (*dinocerts.CertAuthority, []byte, error) {
	rootCa, err := dinocerts.GetRootCertAuthority()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get root dino ca")
	}

	fetchedClusterCa, err := rootCa.MakeIntermediaryCA("cluster-" + clusterID[:8])
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get cluster dino ca")
	}

	return fetchedClusterCa, rootCa.CertPem, nil
}

func (d *Deployer) setupNodeCertificates(
	ctx context.Context,
	node *NodeInfo,
	clusterCa *dinocerts.CertAuthority,
	rootCaPem []byte,
) error {
	nodeIP := net.ParseIP(node.IPAddress)

	var dnsNames []string
	if node.DnsName != "" {
		dnsNames = append(dnsNames, node.DnsName)
	}
	if node.DnsSuffix != "" {
		dnsNames = append(dnsNames, node.DnsSuffix)
	}

	d.logger.Debug("generating node dinocert certificate",
		zap.String("node", node.NodeID),
		zap.Any("IP", nodeIP),
		zap.Any("dnsNames", dnsNames))

	certPem, keyPem, err := clusterCa.MakeServerCertificate("node-"+node.NodeID[:8], []net.IP{nodeIP}, dnsNames)
	if err != nil {
		return errors.Wrap(err, "failed to create server certificate")
	}

	d.logger.Debug("uploading dinocert certificates",
		zap.String("node", node.NodeID))

	var chainPem []byte
	chainPem = append(chainPem, certPem...)
	chainPem = append(chainPem, clusterCa.CertPem...)
	err = d.controller.UploadCertificates(ctx, node.ContainerID, chainPem, keyPem, [][]byte{rootCaPem})
	if err != nil {
		return errors.Wrap(err, "failed to upload certificates")
	}

	nodeCtrl := clustercontrol.NodeManager{
		Endpoint: fmt.Sprintf("http://%s:8091", node.IPAddress),
	}

	d.logger.Debug("refreshing trusted CAs for node",
		zap.String("node", node.NodeID))

	err = nodeCtrl.Controller().LoadTrustedCAs(ctx, &clustercontrol.LoadTrustedCAsOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to load trusted CAs")
	}

	d.logger.Debug("refreshing certificate for node",
		zap.String("node", node.NodeID))

	err = nodeCtrl.Controller().ReloadCertificate(ctx, &clustercontrol.ReloadCertificateOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to refresh certificates")
	}

	d.logger.Debug("removing default self-signed certificate for node",
		zap.String("node", node.NodeID))

	err = nodeCtrl.Controller().DeleteTrustedCA(ctx, &clustercontrol.DeleteTrustedCAOptions{
		ID: 0,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete default certificate")
	}

	return nil
}

func (d *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
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
		err = d.updateDnsRecords(ctx, dnsName, nodes, def.Columnar, nginxIpAddress, true)
		if err != nil {
			return nil, errors.Wrap(err, "failed to update dns records")
		}
	}

	if nginxContainerId != "" {
		useDinoCerts := false
		if clusterCa != nil {
			useDinoCerts = true
		}

		err = d.updateLoadBalancer(ctx, nginxContainerId, nodes, useDinoCerts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to update load balancer")
		}
	}

	// we need to sort the nodes by server version so that the oldest server version
	// is the first one initialized, otherwise in mixed-version clusters, we might
	// end up initializing the higher version nodes first, disallowing older nodes
	// from being initialized into the cluster (couchbase does not permit downgrades).
	// We also sort by IP address next
	slices.SortFunc(nodes, func(a, b *NodeInfo) int {
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

func (d *Deployer) updateLoadBalancer(
	ctx context.Context,
	loadBalancerContainerId string,
	nodes []*NodeInfo,
	enableSsl bool,
) error {
	var addrs []string
	isAnalytics := false
	for _, node := range nodes {
		if node.Type != "server-node" && node.Type != "columnar-node" {
			continue
		}

		if node.Type == "columnar-node" {
			isAnalytics = true
		}

		addrs = append(addrs, node.IPAddress)
	}

	return d.controller.UpdateNginxConfig(ctx, loadBalancerContainerId, addrs, enableSsl, isAnalytics)
}

func (d *Deployer) updateDnsRecords(
	ctx context.Context,
	dnsName string,
	nodes []*NodeInfo,
	isColumnar bool,
	loadBalancerIp string,
	isNewCluster bool,
) error {
	var records []DnsRecord

	var allNodeAs []string
	var allNodeSrvs []string
	var allNodeSecSrvs []string

	for _, node := range nodes {
		if node.Type != "server-node" && node.Type != "columnar-node" {
			continue
		}

		/* don't spam the dns names for now...
		records = append(records, DnsRecord{
			RecordType: "A",
			Name:       node.DnsName,
			Addrs:      []string{node.IPAddress},
		})
		*/

		allNodeAs = append(allNodeAs, node.IPAddress)
		allNodeSrvs = append(allNodeSrvs, "0 0 11210 "+node.IPAddress)
		allNodeSecSrvs = append(allNodeSecSrvs, "0 0 11207 "+node.IPAddress)
	}

	if !isColumnar || loadBalancerIp == "" {
		records = append(records, DnsRecord{
			RecordType: "A",
			Name:       dnsName,
			Addrs:      allNodeAs,
		})
	} else {
		// no need to update the A record for the load balancer on every modify
		if isNewCluster {
			records = append(records, DnsRecord{
				RecordType: "A",
				Name:       dnsName,
				Addrs:      []string{loadBalancerIp},
			})
		}
	}

	if !isColumnar {
		records = append(records, DnsRecord{
			RecordType: "SRV",
			Name:       "_couchbase._tcp.srv." + dnsName,
			Addrs:      allNodeSrvs,
		})

		records = append(records, DnsRecord{
			RecordType: "SRV",
			Name:       "_couchbases._tcp.srv." + dnsName,
			Addrs:      allNodeSecSrvs,
		})
	}

	d.logger.Info("updating dns records", zap.Any("records", records))

	noWait := isNewCluster
	err := d.dnsProvider.UpdateRecords(ctx, records, noWait, false)
	if err != nil {
		return err
	}

	d.logger.Info("records created")
	return nil
}

type deployedNodeInfo struct {
	nodeInfo    *NodeInfo
	ContainerID string
	IPAddress   string
	OTPNode     string
	Version     string
	Services    []clusterdef.Service
}

type deployedClusterInfo struct {
	ID                      string
	Purpose                 string
	Expiry                  time.Time
	Nodes                   []*deployedNodeInfo
	IsColumnar              bool
	DnsSuffix               string
	LoadBalancerContainerID string
	LoadBalancerIPAddress   string
	UseDinoCerts            bool
}

func (d *Deployer) getClusterInfo(ctx context.Context, clusterID string) (*deployedClusterInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var purpose string
	var expiry time.Time
	var isColumnar bool
	var useDinoCerts bool
	var dnsSuffix string
	var loadBalancerContainerID string
	var loadBalancerIPAddress string
	var nodeInfo []*deployedNodeInfo

	for _, node := range nodes {
		if node.ClusterID == clusterID {
			if node.Type == "columnar-node" {
				isColumnar = true
			}

			if node.DnsSuffix != "" {
				dnsSuffix = node.DnsSuffix
			}

			if node.UsingDinoCerts {
				useDinoCerts = true
			}

			if node.Purpose != "" {
				purpose = node.Purpose
			}
			if !node.Expiry.IsZero() && node.Expiry.After(expiry) {
				expiry = node.Expiry
			}

			var otpNode string
			var services []clusterdef.Service

			if node.Type == "server-node" || node.Type == "columnar-node" {
				nodeCtrl := clustercontrol.NodeManager{
					Endpoint: fmt.Sprintf("http://%s:8091", node.IPAddress),
				}
				thisNodeInfo, err := nodeCtrl.Controller().GetLocalInfo(ctx)
				if err != nil {
					return nil, errors.Wrap(err, "failed to list a nodes services")
				}

				services, err = clusterdef.NsServicesToServices(thisNodeInfo.Services)
				if err != nil {
					return nil, errors.Wrap(err, "failed to generate services list")
				}

				otpNode = thisNodeInfo.OTPNode
			}

			if node.Type == "nginx" {
				loadBalancerContainerID = node.ContainerID
				loadBalancerIPAddress = node.IPAddress
			}

			nodeInfo = append(nodeInfo, &deployedNodeInfo{
				nodeInfo:    node,
				ContainerID: node.ContainerID,
				IPAddress:   node.IPAddress,
				OTPNode:     otpNode,
				Version:     node.InitialServerVersion,
				Services:    services,
			})
		}
	}

	return &deployedClusterInfo{
		ID:                      clusterID,
		Purpose:                 purpose,
		Expiry:                  expiry,
		Nodes:                   nodeInfo,
		IsColumnar:              isColumnar,
		DnsSuffix:               dnsSuffix,
		LoadBalancerContainerID: loadBalancerContainerID,
		LoadBalancerIPAddress:   loadBalancerIPAddress,
		UseDinoCerts:            useDinoCerts,
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

	nodesToAddImages, err := d.getImagesForNodeGrps(ctx, nodesToAdd, clusterInfo.IsColumnar)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch images")
	}

	d.logger.Info("deploying new node containers")

	var deployedNodes []*NodeInfo
	var setupNodeOpts []*clustercontrol.AddNodeOptions
	for nodeGrpIdx, nodeGrp := range nodesToAdd {
		image := nodesToAddImages[nodeGrpIdx]

		deployOpts := &DeployNodeOptions{
			Purpose:            clusterInfo.Purpose,
			ClusterID:          clusterInfo.ID,
			Image:              image,
			ImageServerVersion: nodeGrp.Version,
			IsColumnar:         clusterInfo.IsColumnar,
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

	if clusterInfo.UseDinoCerts {
		d.logger.Info("setting up dinocert certificates", zap.String("cluster", clusterInfo.ID))

		clusterCa, rootCaPem, err := d.getClusterDinoCert(clusterInfo.ID)
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

	var postRemovalNodes []*NodeInfo
	for _, clusterNode := range clusterInfo.Nodes {
		if !slices.Contains(nodeIpsBeingRemoved, clusterNode.IPAddress) {
			postRemovalNodes = append(postRemovalNodes, clusterNode.nodeInfo)
		}
	}

	// only need to update if nodes were removed
	if len(nodesToRemove) > 0 {
		// only need to update if dns is enabled
		if clusterInfo.DnsSuffix != "" {
			err = d.updateDnsRecords(ctx, clusterInfo.DnsSuffix, postRemovalNodes, clusterInfo.IsColumnar, clusterInfo.LoadBalancerIPAddress, false)
			if err != nil {
				return nil, errors.Wrap(err, "failed to update dns records")
			}
		}

		if clusterInfo.LoadBalancerContainerID != "" {
			err = d.updateLoadBalancer(ctx, clusterInfo.LoadBalancerContainerID, postRemovalNodes, clusterInfo.UseDinoCerts)
			if err != nil {
				return nil, errors.Wrap(err, "failed to update load balancer")
			}
		}
	}

	otpsToRemove := make([]string, len(nodesToRemove))
	for nodeIdx, nodeToRemove := range nodesToRemove {
		otpsToRemove[nodeIdx] = nodeToRemove.OTPNode
	}

	// once all the new nodes are registered, we re-select a node to work with that is
	// not being removed from the cluster, which can now include the new nodes...

	for _, clusterNode := range clusterInfo.Nodes {
		if clusterNode.nodeInfo.Type != "server-node" &&
			clusterNode.nodeInfo.Type != "columnar-node" {
			continue
		}

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

	if len(nodesToAdd) > 0 {
		thisCluster, err := d.getClusterInfo(ctx, clusterInfo.ID)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get post-rebalance cluster info")
		}

		var allNodes []*NodeInfo
		for _, node := range thisCluster.Nodes {
			allNodes = append(allNodes, node.nodeInfo)
		}

		// no need to update if DNS is disabled
		if thisCluster.DnsSuffix != "" {
			err := d.updateDnsRecords(ctx, thisCluster.DnsSuffix, allNodes, thisCluster.IsColumnar, clusterInfo.LoadBalancerIPAddress, false)
			if err != nil {
				return nil, errors.Wrap(err, "failed to update dns records")
			}
		}

		if thisCluster.LoadBalancerContainerID != "" {
			err := d.updateLoadBalancer(ctx, thisCluster.LoadBalancerContainerID, allNodes, clusterInfo.UseDinoCerts)
			if err != nil {
				return nil, errors.Wrap(err, "failed to update load balancer")
			}
		}
	}

	var deployedNodeIds []string
	for _, node := range deployedNodes {
		deployedNodeIds = append(deployedNodeIds, node.NodeID)
	}

	return deployedNodeIds, nil
}

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	if def.Columnar {
		for _, nodeGrp := range def.NodeGroups {
			if len(nodeGrp.Services) != 0 {
				return errors.New("columnar clusters cannot specify services")
			}

			nodeGrp.Services = []clusterdef.Service{
				clusterdef.KvService,
				clusterdef.AnalyticsService,
			}
		}
	}

	clusterInfo, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return errors.New("cannot modify a cluster with no nodes")
	}

	if len(def.NodeGroups) > 0 {
		nodesToRemove := make([]*deployedNodeInfo, len(clusterInfo.Nodes))
		copy(nodesToRemove, clusterInfo.Nodes)
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

		// remove all utility nodes from auto-deletion
		nodesToRemove = slices.DeleteFunc(nodesToRemove, func(node *deployedNodeInfo) bool {
			isClusterNode := false
			if node.nodeInfo.Type == "server-node" ||
				node.nodeInfo.Type == "columnar-node" {
				isClusterNode = true
			}
			return !isClusterNode
		})

		// first iterate and find any exact matches and use those
		nodesToAdd = slices.DeleteFunc(nodesToAdd, func(nodeGrp *clusterdef.NodeGroup) bool {
			if nodeGrp.ForceNew {
				return false
			}

			for nodeIdx, node := range nodesToRemove {
				if node.Version != nodeGrp.Version {
					continue
				}

				nodeGrpServices := nodeGrp.Services
				if len(nodeGrpServices) == 0 {
					nodeGrpServices = DEFAULT_SERVICES
				}

				serviceCmp := clusterdef.CompareServices(node.Services, nodeGrpServices)
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

func (d *Deployer) appendNodeDnsNames(dnsNames []string, node *NodeInfo) []string {
	if node.DnsName != "" {
		if !slices.Contains(dnsNames, node.DnsName) {
			dnsNames = append(dnsNames, node.DnsName)
		}
	}
	if node.DnsSuffix != "" {
		couchbaseRec := "_couchbase._tcp.srv." + node.DnsSuffix
		couchbasesRec := "_couchbases._tcp.srv." + node.DnsSuffix

		if !slices.Contains(dnsNames, node.DnsSuffix) {
			dnsNames = append(dnsNames, node.DnsSuffix)
		}
		if !slices.Contains(dnsNames, couchbaseRec) {
			dnsNames = append(dnsNames, couchbaseRec)
		}
		if !slices.Contains(dnsNames, couchbasesRec) {
			dnsNames = append(dnsNames, couchbasesRec)
		}
	}

	return dnsNames
}

func (d *Deployer) removeDnsNames(ctx context.Context, dnsNames []string) {
	if len(dnsNames) > 0 {
		if d.dnsProvider == nil {
			d.logger.Warn("could not remove associated dns names due to no dns configuration")
		} else {
			d.logger.Info("removing dns names", zap.Any("names", dnsNames))
			err := d.dnsProvider.RemoveRecords(ctx, dnsNames, true, true)
			if err != nil {
				d.logger.Warn("failed to remove dns names", zap.Error(err))
			}
		}
	}
}

func (d *Deployer) removeNodes(ctx context.Context, nodes []*NodeInfo) error {
	var dnsToRemove []string
	for _, node := range nodes {
		dnsToRemove = d.appendNodeDnsNames(dnsToRemove, node)
	}

	waitCh := make(chan error)
	for _, node := range nodes {
		go func(node *NodeInfo) {
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

func (d *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	var nodesToRemove []*NodeInfo
	for _, node := range nodes {
		if node.ClusterID == clusterID {
			nodesToRemove = append(nodesToRemove, node)
		}
	}

	return d.removeNodes(ctx, nodesToRemove)
}

func (d *Deployer) RemoveAll(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	return d.removeNodes(ctx, nodes)
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

	if thisCluster.DnsName != "" {
		return &deployment.ConnectInfo{
			ConnStr:        fmt.Sprintf("couchbase://%s", "srv."+thisCluster.DnsName),
			ConnStrTls:     fmt.Sprintf("couchbases://%s", "srv."+thisCluster.DnsName),
			Analytics:      fmt.Sprintf("http://%s:8095", thisCluster.DnsName),
			AnalyticsTls:   fmt.Sprintf("https://%s:18095", thisCluster.DnsName),
			Mgmt:           fmt.Sprintf("http://%s", thisCluster.DnsName),
			MgmtTls:        fmt.Sprintf("https://%s", thisCluster.DnsName),
			DataApiConnstr: "",
		}, nil
	}

	var connstrAddrs []string
	var connstrTlsAddrs []string
	var mgmtAddr string
	var mgmtTlsAddr string
	var analyticsAddr string
	var analyticsTlsAddr string
	for _, node := range thisCluster.Nodes {
		if !node.IsClusterNode() {
			continue
		}

		kvPort := 11210
		kvTlsPort := 11207
		mgmtPort := 8091
		mgmtTlsPort := 18091
		analyticsPort := 8095
		analyticsTlsPort := 18095

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
		analyticsAddr = fmt.Sprintf("%s:%d", node.IPAddress, analyticsPort)
		analyticsTlsAddr = fmt.Sprintf("%s:%d", node.IPAddress, analyticsTlsPort)
	}

	connStr := fmt.Sprintf("couchbase://%s", strings.Join(connstrAddrs, ","))
	connStrTls := fmt.Sprintf("couchbases://%s", strings.Join(connstrTlsAddrs, ","))
	analytics := fmt.Sprintf("http://%s", analyticsAddr)
	analyticsTls := fmt.Sprintf("https://%s", analyticsTlsAddr)
	mgmt := fmt.Sprintf("http://%s", mgmtAddr)
	mgmtTls := fmt.Sprintf("https://%s", mgmtTlsAddr)

	lbIp := thisCluster.LoadBalancerIPAddress
	if lbIp != "" {
		analytics = fmt.Sprintf("http://%s:8095", lbIp)
		analyticsTls = fmt.Sprintf("https://%s:18095", lbIp)
		mgmt = fmt.Sprintf("http://%s:8091", lbIp)
		mgmtTls = fmt.Sprintf("https://%s:8091", lbIp)
	}

	return &deployment.ConnectInfo{
		ConnStr:        connStr,
		ConnStrTls:     connStrTls,
		Analytics:      analytics,
		AnalyticsTls:   analyticsTls,
		Mgmt:           mgmt,
		MgmtTls:        mgmtTls,
		DataApiConnstr: "",
	}, nil
}

func (d *Deployer) Cleanup(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return err
	}

	curTime := time.Now()

	var nodesToRemove []*NodeInfo
	for _, node := range nodes {
		if !node.Expiry.IsZero() && !node.Expiry.After(curTime) {
			nodesToRemove = append(nodesToRemove, node)
		}
	}

	return d.removeNodes(ctx, nodesToRemove)
}

func (d *Deployer) DestroyAllResources(ctx context.Context) error {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list all nodes")
	}

	return d.removeNodes(ctx, nodes)
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

	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster controller")
	}

	if clusterInfo.Type == deployment.ClusterTypeColumnar {
		err = controller.Controller().LoadAnalyticsSampleBucket(ctx, bucketName)
	} else {
		err = controller.Controller().LoadSampleBucket(ctx, bucketName)
	}

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
	cluster, err := d.getClusterInfo(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster info")
	}

	if cluster.UseDinoCerts {
		clusterCa, _, err := d.getClusterDinoCert(clusterID)
		if err != nil {
			return "", errors.Wrap(err, "failed to get cluster CA")
		}

		return string(clusterCa.CertPem), nil
	}

	controller, err := d.getController(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster controller")
	}

	var certPem string

	resp, err := controller.Controller().GetTrustedCAs(ctx)
	if err != nil {
		d.logger.Warn("failed to get trusted CAs, trying to fall back to get-certificate", zap.Error(err))

		cresp, cerr := controller.Controller().GetCertificate(ctx)
		if cerr != nil {
			return "", errors.Wrap(cerr, "failed to get certificate")
		}

		certPem = strings.TrimSpace(string(*cresp))
	} else {
		lastCert := (*resp)[len(*resp)-1]
		certPem = strings.TrimSpace(lastCert.Pem)
	}

	return certPem, nil
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

func (d *Deployer) RedeployCluster(ctx context.Context, clusterID string) error {
	return errors.New("docker deploy does not support redeploy cluster")
}

func (d *Deployer) CreateCapellaLink(ctx context.Context, columnarID, linkName, clusterId, directID string) error {
	return errors.New("docker deploy does not support create capella link")
}

func (d *Deployer) CreateS3Link(ctx context.Context, columnarID, linkName, region, endpoint, accessKey, secretKey string) error {
	return errors.New("docker deploy does not support create S3 link")
}

func (d *Deployer) DropLink(ctx context.Context, columnarID, linkName string) error {
	return errors.New("docker deploy does not support drop link")
}

func (d *Deployer) UpgradeCluster(ctx context.Context, clusterID string, CurrentImages string, NewImage string) error {
	return errors.New("docker deploy does not support upgrade cluster command")
}

func (d *Deployer) EnableDataApi(ctx context.Context, clusterID string) error {
	return errors.New("docker deploy does not support enabling data api")
}
