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
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/exp/slices"
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

func (d *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	clusters, err := d.listClusters(ctx)
	if err != nil {
		return nil, err
	}

	var out []deployment.ClusterInfo
	for _, cluster := range clusters {
		out = append(out, d.clusterInfoFromCluster(cluster))
	}
	return out, nil
}

func (d *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	clusterInfo, err := d.newCluster(ctx, def)
	if err != nil {
		return nil, err
	}

	return d.clusterInfoFromCluster(clusterInfo), nil
}

func (d *Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	clusterInfoEx, err := d.getClusterInfoEx(ctx, clusterInfo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get extended cluster info")
	}

	var nodeGroups []*clusterdef.NodeGroup

	for _, node := range clusterInfoEx.NodesEx {
		nodeGroups = append(nodeGroups, &clusterdef.NodeGroup{
			Count:    1,
			Version:  node.InitialServerVersion,
			Services: node.Services,
		})
	}

	return &clusterdef.Cluster{
		Purpose:    clusterInfo.Purpose,
		NodeGroups: nodeGroups,
	}, nil
}

func (d *Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
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

	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return errors.New("cannot modify a cluster with no nodes")
	}

	clusterInfoEx, err := d.getClusterInfoEx(ctx, clusterInfo)
	if err != nil {
		return errors.Wrap(err, "failed to get extended cluster info")
	}

	if len(def.NodeGroups) > 0 {
		nodesToRemove := make([]*nodeInfoEx, len(clusterInfo.Nodes))
		copy(nodesToRemove, clusterInfoEx.NodesEx)
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
		nodesToRemove = slices.DeleteFunc(nodesToRemove, func(node *nodeInfoEx) bool {
			return !node.IsClusterNode()
		})

		// first iterate and find any exact matches and use those
		nodesToAdd = slices.DeleteFunc(nodesToAdd, func(nodeGrp *clusterdef.NodeGroup) bool {
			if nodeGrp.ForceNew {
				return false
			}

			for nodeIdx, node := range nodesToRemove {
				if node.InitialServerVersion != nodeGrp.Version {
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

		_, err := d.addRemoveNodes(ctx, clusterInfoEx, nodesToAdd, nodesToRemove)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Deployer) AddNode(ctx context.Context, clusterID string) (string, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster info")
	}

	clusterInfoEx, err := d.getClusterInfoEx(ctx, clusterInfo)
	if err != nil {
		return "", errors.Wrap(err, "failed to get extended cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return "", errors.New("cannot add a node to a cluster with no nodes")
	}

	nodeVersion := clusterInfoEx.NodesEx[0].InitialServerVersion
	nodeServices := clusterInfoEx.NodesEx[0].Services

	for _, node := range clusterInfoEx.NodesEx {
		if nodeVersion != node.InitialServerVersion || slices.Compare(nodeServices, node.Services) != 0 {
			return "", errors.New("cluster must have homogenous versions to add a node")
		}
	}

	nodeIds, err := d.addRemoveNodes(ctx, clusterInfoEx, []*clusterdef.NodeGroup{
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
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	clusterInfoEx, err := d.getClusterInfoEx(ctx, clusterInfo)
	if err != nil {
		return errors.Wrap(err, "failed to get extended cluster info")
	}

	// we find the node the user selected, and a secondary node that we
	// can use to actually manipulate the cluster
	var foundNode *nodeInfoEx
	for _, clusterNode := range clusterInfoEx.NodesEx {
		if clusterNode.ContainerID == nodeID {
			foundNode = clusterNode
		}
	}
	if foundNode == nil {
		return errors.Wrap(err, "failed to find deployed node")
	}

	_, err = d.addRemoveNodes(ctx, clusterInfoEx, nil, []*nodeInfoEx{
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

	var nodesToRemove []*ContainerInfo
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

func (d *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	thisCluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	if thisCluster.DnsName != "" {
		dnsAName := ""
		dnsSRVName := ""
		// SRV records for normal CB server
		if thisCluster.Type == deployment.ClusterTypeServer {
			dnsSRVName = "srv." + thisCluster.DnsName
		} else if thisCluster.Type == deployment.ClusterTypeColumnar {
			dnsAName = thisCluster.DnsName
		}
		return &deployment.ConnectInfo{
			ConnStr:        fmt.Sprintf("couchbase://%s", "srv."+thisCluster.DnsName),
			ConnStrTls:     fmt.Sprintf("couchbases://%s", "srv."+thisCluster.DnsName),
			Analytics:      fmt.Sprintf("http://%s:8095", thisCluster.DnsName),
			AnalyticsTls:   fmt.Sprintf("https://%s:18095", thisCluster.DnsName),
			Mgmt:           fmt.Sprintf("http://%s", thisCluster.DnsName),
			MgmtTls:        fmt.Sprintf("https://%s", thisCluster.DnsName),
			DataApiConnstr: "",
			DnsAName:       dnsAName,
			DnsSRVName:     dnsSRVName,
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

	lbIp := thisCluster.LoadBalancerIPAddress()
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

	var nodesToRemove []*ContainerInfo
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
		Logger:   d.logger,
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
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	resp, err := agent.GetAllBuckets(ctx, &cbmgmtx.GetAllBucketsOptions{})
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
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	ramQuotaMb := 256
	if opts.RamQuotaMB > 0 {
		ramQuotaMb = opts.RamQuotaMB
	}

	numReplicas := 1
	if opts.NumReplicas > 1 {
		numReplicas = opts.NumReplicas
	}

	err = agent.CreateBucket(ctx, &cbmgmtx.CreateBucketOptions{
		BucketName: opts.Name,
		BucketSettings: cbmgmtx.BucketSettings{
			BucketType:             "membase",
			StorageBackend:         "couchstore",
			ReplicaIndex:           false,
			ConflictResolutionType: "seqno",
			MutableBucketSettings: cbmgmtx.MutableBucketSettings{
				EvictionPolicy:     "valueOnly",
				ReplicaNumber:      uint32(numReplicas),
				DurabilityMinLevel: "none",
				CompressionMode:    "passive",
				MaxTTL:             0,
				RAMQuotaMB:         uint64(ramQuotaMb),
				FlushEnabled:       opts.FlushEnabled,
			},
		},
	})
	if err != nil {
		return errors.Wrap(err, "failed to create bucket")
	}

	err = agent.EnsureBucket(ctx, &gocbcorex.EnsureBucketOptions{
		BucketName:  opts.Name,
		WantHealthy: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bucket")
	}

	return nil
}

func (d *Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	agent, err := d.getAgent(ctx, clusterID, "")
	if err != nil {
		return errors.Wrap(err, "failed to get cluster agent")
	}
	defer agent.Close()

	agent.DeleteBucket(ctx, &cbmgmtx.DeleteBucketOptions{
		BucketName: bucketName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete bucket")
	}

	err = agent.EnsureBucket(ctx, &gocbcorex.EnsureBucketOptions{
		BucketName:  bucketName,
		WantMissing: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bucket")
	}

	return nil
}

func (d *Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	cluster, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get cluster info")
	}

	if cluster.UsingDinoCerts {
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

func (d *Deployer) getNode(ctx context.Context, clusterID, nodeID string) (*ContainerInfo, error) {
	nodes, err := d.controller.ListNodes(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list nodes")
	}

	var foundNode *ContainerInfo
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

func (d *Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string, trafficType deployment.BlockNodeTrafficType, rejectType string) error {
	var nodeContainerIDs []string
	for _, nodeId := range nodeIDs {
		node, err := d.getNode(ctx, clusterID, nodeId)
		if err != nil {
			return errors.Wrap(err, "failed to get node")
		}

		nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
	}
	if len(nodeIDs) == 0 {
		clusterInfo, err := d.getCluster(ctx, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to get cluster info")
		}

		for _, node := range clusterInfo.Nodes {
			nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
		}
	}

	var tcType TrafficControlType
	switch trafficType {
	case deployment.BlockNodeTrafficNodes:
		tcType = TrafficControlBlockNodes
	case deployment.BlockNodeTrafficClients:
		tcType = TrafficControlBlockClients
	case deployment.BlockNodeTrafficAll:
		tcType = TrafficControlBlockAll
	}

	for _, nodeContainerID := range nodeContainerIDs {
		err := d.controller.SetTrafficControl(ctx, nodeContainerID, tcType, rejectType, nil, nil)
		if err != nil {
			return errors.Wrap(err, "failed to block traffic")
		}
	}

	return nil
}

func (d *Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string) error {
	var nodeContainerIDs []string
	for _, nodeId := range nodeIDs {
		node, err := d.getNode(ctx, clusterID, nodeId)
		if err != nil {
			return errors.Wrap(err, "failed to get node")
		}

		nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
	}
	if len(nodeIDs) == 0 {
		clusterInfo, err := d.getCluster(ctx, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to get cluster info")
		}

		for _, node := range clusterInfo.Nodes {
			nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
		}
	}

	for _, nodeContainerID := range nodeContainerIDs {
		err := d.controller.SetTrafficControl(ctx, nodeContainerID, TrafficControlAllowAll, "", nil, nil)
		if err != nil {
			return errors.Wrap(err, "failed to allow traffic")
		}
	}

	return nil
}

func (d *Deployer) PartitionNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string, rejectType string) error {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster info")
	}

	var islandNodes []*nodeInfo
	for _, node := range clusterInfo.Nodes {
		if slices.Contains(nodeIDs, node.NodeID) {
			islandNodes = append(islandNodes, node)
		}
	}

	if len(islandNodes) != len(nodeIDs) {
		return fmt.Errorf("not all nodes found in cluster %s: %v", clusterID, nodeIDs)
	}

	var islandAllowedIps []string
	// allow the nodes on the island to communicate with each other
	for _, node := range islandNodes {
		islandAllowedIps = append(islandAllowedIps, node.IPAddress)
	}
	// allow non-node traffic to communicate with the island nodes
	for _, node := range clusterInfo.Nodes {
		if !node.IsClusterNode() {
			islandAllowedIps = append(islandAllowedIps, node.IPAddress)
		}
	}

	d.logger.Info("partitioning traffic for nodes",
		zap.Strings("islandAllowedIps", islandAllowedIps))

	for _, node := range islandNodes {
		d.logger.Debug("partitioning traffic for node",
			zap.String("nodeID", node.NodeID),
			zap.String("containerID", node.ContainerID),
			zap.String("ipAddress", node.IPAddress))

		// block all inter-node traffic for this specific node, but allow traffic from the partition
		err := d.controller.SetTrafficControl(ctx, node.ContainerID, TrafficControlBlockNodes, rejectType, nil, islandAllowedIps)
		if err != nil {
			return errors.Wrapf(err, "failed to partition traffic for node %s", node.NodeID)
		}
	}

	return nil
}

func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	clusterInfo, err := d.getCluster(ctx, clusterID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster info")
	}

	if len(clusterInfo.Nodes) == 0 {
		return nil, errors.New("cannot collection logs from a cluster with no nodes")
	}

	nodeCtrl := clustercontrol.NodeManager{
		Logger:   d.logger,
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

	nodeInfoFromIp := func(ipAddress string) *nodeInfo {
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

func (d *Deployer) PauseNode(ctx context.Context, clusterID string, nodeIDs []string) error {
	var nodeContainerIDs []string
	for _, nodeId := range nodeIDs {
		node, err := d.getNode(ctx, clusterID, nodeId)
		if err != nil {
			return errors.Wrap(err, "failed to get node")
		}

		nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
	}
	if len(nodeIDs) == 0 {
		clusterInfo, err := d.getCluster(ctx, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to get cluster info")
		}

		for _, node := range clusterInfo.Nodes {
			nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
		}
	}

	for _, nodeContainerID := range nodeContainerIDs {
		err := d.dockerCli.ContainerPause(ctx, nodeContainerID)
		if err != nil {
			return errors.Wrap(err, "failed to pause node")
		}
	}

	return nil
}

func (d *Deployer) UnpauseNode(ctx context.Context, clusterID string, nodeIDs []string) error {
	var nodeContainerIDs []string
	for _, nodeId := range nodeIDs {
		node, err := d.getNode(ctx, clusterID, nodeId)
		if err != nil {
			return errors.Wrap(err, "failed to get node")
		}

		nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
	}
	if len(nodeIDs) == 0 {
		clusterInfo, err := d.getCluster(ctx, clusterID)
		if err != nil {
			return errors.Wrap(err, "failed to get cluster info")
		}

		for _, node := range clusterInfo.Nodes {
			nodeContainerIDs = append(nodeContainerIDs, node.ContainerID)
		}
	}

	for _, nodeContainerID := range nodeContainerIDs {
		err := d.dockerCli.ContainerUnpause(ctx, nodeContainerID)
		if err != nil {
			return errors.Wrap(err, "failed to pause node")
		}
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
