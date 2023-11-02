package deployment

import (
	"context"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
)

type ClusterNodeInfo interface {
	GetID() string
	GetResourceID() string
	GetName() string
	GetIPAddress() string
}

type ClusterInfo interface {
	GetID() string
	GetPurpose() string
	GetExpiry() time.Time
	GetState() string
	GetNodes() []ClusterNodeInfo
}

type ConnectInfo struct {
	ConnStr string
	Mgmt    string
}

type UserInfo struct {
	Username string
	CanRead  bool
	CanWrite bool
}

type CreateUserOptions struct {
	Username string
	Password string
	CanRead  bool
	CanWrite bool
}

type BucketInfo struct {
	Name string
}

type CreateBucketOptions struct {
	Name       string
	RamQuotaMB int
}

type Deployer interface {
	ListClusters(ctx context.Context) ([]ClusterInfo, error)
	NewCluster(ctx context.Context, def *clusterdef.Cluster) (ClusterInfo, error)
	GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error)
	ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error
	RemoveCluster(ctx context.Context, clusterID string) error
	RemoveAll(ctx context.Context) error
	Cleanup(ctx context.Context) error
	GetConnectInfo(ctx context.Context, clusterID string) (*ConnectInfo, error)
	ListUsers(ctx context.Context, clusterID string) ([]UserInfo, error)
	CreateUser(ctx context.Context, clusterID string, opts *CreateUserOptions) error
	DeleteUser(ctx context.Context, clusterID string, username string) error
	ListBuckets(ctx context.Context, clusterID string) ([]BucketInfo, error)
	CreateBucket(ctx context.Context, clusterID string, opts *CreateBucketOptions) error
	DeleteBucket(ctx context.Context, clusterID string, bucketName string) error
	GetCertificate(ctx context.Context, clusterID string) (string, error)
}
