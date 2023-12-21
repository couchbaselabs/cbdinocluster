package caodeploy

import (
	"context"
	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"time"
)

type Deployer struct {
	logger *zap.Logger
	client *kubernetes.Clientset
}

// AllowNodeTraffic implements deployment.Deployer.
func (*Deployer) AllowNodeTraffic(ctx context.Context, clusterID string, nodeID string) error {
	panic("unimplemented")
}

// BlockNodeTraffic implements deployment.Deployer.
func (*Deployer) BlockNodeTraffic(ctx context.Context, clusterID string, nodeID string) error {
	panic("unimplemented")
}

// Cleanup implements deployment.Deployer.
func (*Deployer) Cleanup(ctx context.Context) error {
	panic("unimplemented")
}

// CollectLogs implements deployment.Deployer.
func (*Deployer) CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error) {
	panic("unimplemented")
}

// CreateBucket implements deployment.Deployer.
func (*Deployer) CreateBucket(ctx context.Context, clusterID string, opts *deployment.CreateBucketOptions) error {
	panic("unimplemented")
}

// CreateCollection implements deployment.Deployer.
func (*Deployer) CreateCollection(ctx context.Context, clusterID string, bucketName string, scopeName string, collectionName string) error {
	panic("unimplemented")
}

// CreateScope implements deployment.Deployer.
func (*Deployer) CreateScope(ctx context.Context, clusterID string, bucketName string, scopeName string) error {
	panic("unimplemented")
}

// CreateUser implements deployment.Deployer.
func (*Deployer) CreateUser(ctx context.Context, clusterID string, opts *deployment.CreateUserOptions) error {
	panic("unimplemented")
}

// DeleteBucket implements deployment.Deployer.
func (*Deployer) DeleteBucket(ctx context.Context, clusterID string, bucketName string) error {
	panic("unimplemented")
}

// DeleteCollection implements deployment.Deployer.
func (*Deployer) DeleteCollection(ctx context.Context, clusterID string, bucketName string, scopeName string, collectionName string) error {
	panic("unimplemented")
}

// DeleteScope implements deployment.Deployer.
func (*Deployer) DeleteScope(ctx context.Context, clusterID string, bucketName string, scopeName string) error {
	panic("unimplemented")
}

// DeleteUser implements deployment.Deployer.
func (*Deployer) DeleteUser(ctx context.Context, clusterID string, username string) error {
	panic("unimplemented")
}

// ExecuteQuery implements deployment.Deployer.
func (*Deployer) ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error) {
	panic("unimplemented")
}

// GetCertificate implements deployment.Deployer.
func (*Deployer) GetCertificate(ctx context.Context, clusterID string) (string, error) {
	panic("unimplemented")
}

// GetConnectInfo implements deployment.Deployer.
func (*Deployer) GetConnectInfo(ctx context.Context, clusterID string) (*deployment.ConnectInfo, error) {
	panic("unimplemented")
}

// GetDefinition implements deployment.Deployer.
func (*Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	panic("unimplemented")
}

// ListBuckets implements deployment.Deployer.
func (*Deployer) ListBuckets(ctx context.Context, clusterID string) ([]deployment.BucketInfo, error) {
	panic("unimplemented")
}

// ListClusters implements deployment.Deployer.
func (*Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	panic("unimplemented")
}

// ListCollections implements deployment.Deployer.
func (*Deployer) ListCollections(ctx context.Context, clusterID string, bucketName string) ([]deployment.ScopeInfo, error) {
	panic("unimplemented")
}

// ListImages implements deployment.Deployer.
func (*Deployer) ListImages(ctx context.Context) ([]deployment.Image, error) {
	panic("unimplemented")
}

// ListUsers implements deployment.Deployer.
func (*Deployer) ListUsers(ctx context.Context, clusterID string) ([]deployment.UserInfo, error) {
	panic("unimplemented")
}

// ModifyCluster implements deployment.Deployer.
func (*Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	panic("unimplemented")
}

// NewCluster implements deployment.Deployer.
func (*Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	panic("unimplemented")
}

// RemoveAll implements deployment.Deployer.
func (*Deployer) RemoveAll(ctx context.Context) error {
	panic("unimplemented")
}

// RemoveCluster implements deployment.Deployer.
func (*Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	panic("unimplemented")
}

// SearchImages implements deployment.Deployer.
func (*Deployer) SearchImages(ctx context.Context, version string) ([]deployment.Image, error) {
	panic("unimplemented")
}

// UpdateClusterExpiry implements deployment.Deployer.
func (*Deployer) UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error {
	panic("unimplemented")
}

var _ deployment.Deployer = (*Deployer)(nil)

type NewDeployerOptions struct {
	Logger *zap.Logger
	Client *kubernetes.Clientset
}

func NewDeployer(opts *NewDeployerOptions) (*Deployer, error) {
	return &Deployer{
		logger: opts.Logger,
		client: opts.Client,
	}, nil
}
