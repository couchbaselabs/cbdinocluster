package localdeploy

import (
	"context"
	"runtime"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Deployer struct {
	logger *zap.Logger
}

var _ deployment.Deployer = (*Deployer)(nil)

type DeployerOptions struct {
	Logger *zap.Logger
}

func NewDeployer(opts *DeployerOptions) (*Deployer, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("localdeploy is only supported on macOS")
	}

	return &Deployer{
		logger: opts.Logger,
	}, nil
}

func (d *Deployer) controller() *OsxController {
	return &OsxController{
		Logger: d.logger,
	}
}

func (d *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	isInstalled, err := d.controller().IsInstalled(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check if couchbase is installed")
	}

	if !isInstalled {
		return nil, nil
	}

	isRunning, err := d.controller().IsRunning(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check if couchbase is running")
	}

	if !isRunning {
		return nil, nil
	}

	return []deployment.ClusterInfo{
		&ClusterInfo{},
	}, nil
}

func (d *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	if len(def.NodeGroups) != 1 || def.NodeGroups[0].Count != 1 {
		return nil, errors.New("local deployment only supports a single node")
	}
	if def.Columnar {
		return nil, errors.New("columnar is not supported for local deploy")
	}

	nodeGrp := def.NodeGroups[0]

	versionInfo, err := versionident.Identify(ctx, nodeGrp.Version)
	if err != nil {
		return nil, errors.Wrap(err, "failed to identify version")
	}

	err = d.controller().Start(ctx, &ServerDef{
		Version:             versionInfo.Version,
		BuildNo:             versionInfo.BuildNo,
		UseCommunityEdition: versionInfo.CommunityEdition,
		UseServerless:       versionInfo.Serverless,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start cluster")
	}

	return &ClusterInfo{}, nil
}

func (d *Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	return nil, errors.New("localdeploy does not support fetching the cluster definition")
}

func (d *Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	return errors.New("localdeploy does not support updating expiry")
}

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	return errors.New("localdeploy does not support cluster modification")
}

func (d *Deployer) AddNode(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("localdeploy does not support cluster node addition")
}

func (d *Deployer) RemoveNode(ctx context.Context, clusterID string, nodeID string) error {
	return errors.New("localdeploy does not support cluster node removal")
}

func (d *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	if clusterID != "local" {
		return errors.New("invalid cluster-id")
	}

	err := d.controller().Stop(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to stop cluster")
	}

	return nil
}

func (d *Deployer) RemoveAll(ctx context.Context) error {
	err := d.controller().Stop(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to stop cluster")
	}

	return nil
}

func (d *Deployer) Cleanup(ctx context.Context) error {
	return nil
}

func (d *Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	return &deployment.ConnectInfo{
		ConnStr:    "couchbase://127.0.0.1",
		ConnStrTls: "couchbases://127.0.0.1",
		Mgmt:       "http://127.0.0.1:8091",
		MgmtTls:    "https://127.0.0.1:18091",
	}, nil
}

func (d *Deployer) ListUsers(ctx context.Context, clusterID string) ([]deployment.UserInfo, error) {
	return nil, errors.New("localdeploy does not support user modification")
}

func (d *Deployer) CreateUser(ctx context.Context, clusterID string, opts *deployment.CreateUserOptions) error {
	return errors.New("localdeploy does not support user management")
}

func (d *Deployer) DeleteUser(ctx context.Context, clusterID string, username string) error {
	return errors.New("localdeploy does not support user management")
}

func (d *Deployer) ListBuckets(ctx context.Context, clusterID string) ([]deployment.BucketInfo, error) {
	return nil, errors.New("localdeploy does not support bucket management")
}

func (d *Deployer) CreateBucket(ctx context.Context, clusterID string, opts *deployment.CreateBucketOptions) error {
	return errors.New("localdeploy does not support user management")
}

func (d *Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	return errors.New("localdeploy does not support user management")
}

func (d *Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("localdeploy does not support getting the CA certificate")
}

func (d *Deployer) GetGatewayCertificate(ctx context.Context, clusterID string) (string, error) {
	return "", errors.New("localdeploy does not support getting gateway certificates")
}

func (d *Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error) {
	return "", errors.New("localdeploy does not support executing queries")
}

func (d *Deployer) ListCollections(ctx context.Context, clusterID string, bucketName string) ([]deployment.ScopeInfo, error) {
	return nil, errors.New("localdeploy does not support getting collections")
}

func (d *Deployer) CreateScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	return errors.New("localdeploy does not support creating scopes")
}

func (d *Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	return errors.New("localdeploy does not support creating collections")
}

func (d *Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName, scopeName string) error {
	return errors.New("localdeploy does not support deleting scopes")
}

func (d *Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error {
	return errors.New("localdeploy does not support deleting collections")
}

func (d *Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string, trafficType deployment.BlockNodeTrafficType, rejectType string) error {
	return errors.New("localdeploy does not support traffic control")
}

func (d *Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("localdeploy does not support traffic control")
}

func (d *Deployer) PartitionNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string, rejectType string) error {
	return errors.New("localdeploy does not support traffic control")
}

func (d *Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	return nil, errors.New("localdeploy does not support log collection")
}

func (d *Deployer) ListImages(ctx context.Context) ([]deployment.Image, error) {
	return nil, errors.New("localdeploy does not support image listing")
}

func (d *Deployer) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	return nil, errors.New("localdeploy does not support image search")
}

func (d *Deployer) PauseNode(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("localdeploy does not support node pausing")
}

func (d *Deployer) UnpauseNode(ctx context.Context, clusterID string, nodeIDs []string) error {
	return errors.New("localdeploy does not support node pausing")
}

func (d *Deployer) LoadSampleBucket(ctx context.Context, clusterID string, bucketName string) error {
	return errors.New("localdeploy does not support loading sample buckets")
}

func (d *Deployer) RedeployCluster(ctx context.Context, clusterID string) error {
	return errors.New("localdeploy does not support redeploy cluster")
}

func (d *Deployer) CreateCapellaLink(ctx context.Context, columnarID, linkName, clusterId, directID string) error {
	return errors.New("localdeploy does not support create capella link")
}

func (d *Deployer) CreateS3Link(ctx context.Context, columnarID, linkName, region, endpoint, accessKey, secretKey string) error {
	return errors.New("localdeploy does not support create S3 link")
}

func (d *Deployer) DropLink(ctx context.Context, columnarID, linkName string) error {
	return errors.New("localdeploy does not support drop link")
}

func (d *Deployer) UpgradeCluster(ctx context.Context, clusterID string, CurrentImages string, NewImage string) error {
	return errors.New("localdeploy does not support upgrade cluster command")
}

func (d *Deployer) EnableDataApi(ctx context.Context, clusterID string) error {
	return errors.New("localdeploy does not support enabling data api")
}
