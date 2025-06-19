package deployment

import (
	"context"
	"time"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
)

type ClusterType string

const (
	ClusterTypeUnknown  ClusterType = "unknown"
	ClusterTypeServer   ClusterType = "server"
	ClusterTypeColumnar ClusterType = "columnar"
)

type ClusterNodeInfo interface {
	GetID() string
	IsClusterNode() bool
	GetResourceID() string
	GetName() string
	GetIPAddress() string
}

type ClusterInfo interface {
	GetID() string
	GetType() ClusterType
	GetPurpose() string
	GetExpiry() time.Time
	GetState() string
	GetNodes() []ClusterNodeInfo
}

type ConnectInfo struct {
	ConnStr        string
	ConnStrTls     string
	ConnStrCb2     string
	Analytics      string
	AnalyticsTls   string
	Mgmt           string
	MgmtTls        string
	DataApiConnstr string
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
	Name         string
	RamQuotaMB   int
	FlushEnabled bool
	NumReplicas  int
}

type ScopeInfo struct {
	Name        string
	Collections []CollectionInfo
}

type CollectionInfo struct {
	Name string
}

type Image struct {
	Source     string
	Name       string
	SourcePath string
}

type BlockNodeTrafficType string

const (
	BlockNodeTrafficClients BlockNodeTrafficType = "clients"
	BlockNodeTrafficNodes   BlockNodeTrafficType = "nodes"
	BlockNodeTrafficAll     BlockNodeTrafficType = "all"
)

type Deployer interface {
	ListClusters(ctx context.Context) ([]ClusterInfo, error)
	NewCluster(ctx context.Context, def *clusterdef.Cluster) (ClusterInfo, error)
	GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error)
	UpdateClusterExpiry(ctx context.Context, clusterID string, newExpiryTime time.Time) error
	ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error
	UpgradeCluster(ctx context.Context, clusterID string, CurrentImages string, NewImage string) error
	AddNode(ctx context.Context, clusterID string) (string, error)
	RemoveNode(ctx context.Context, clusterID string, nodeID string) error
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
	LoadSampleBucket(ctx context.Context, clusterID string, bucketName string) error
	GetCertificate(ctx context.Context, clusterID string) (string, error)
	GetGatewayCertificate(ctx context.Context, clusterID string) (string, error)
	ExecuteQuery(ctx context.Context, clusterID string, query string) (string, error)
	ListCollections(ctx context.Context, clusterID string, bucketName string) ([]ScopeInfo, error)
	CreateScope(ctx context.Context, clusterID string, bucketName, scopeName string) error
	CreateCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error
	DeleteScope(ctx context.Context, clusterID string, bucketName, scopeName string) error
	DeleteCollection(ctx context.Context, clusterID string, bucketName, scopeName, collectionName string) error
	BlockNodeTraffic(ctx context.Context, clusterID string, nodeID string, blockType BlockNodeTrafficType) error
	AllowNodeTraffic(ctx context.Context, clusterID string, nodeID string) error
	PartitionNodeTraffic(ctx context.Context, clusterID string, nodeIDs []string) error
	CollectLogs(ctx context.Context, clusterID string, destPath string) ([]string, error)
	ListImages(ctx context.Context) ([]Image, error)
	SearchImages(ctx context.Context, version string) ([]Image, error)
	PauseNode(ctx context.Context, clusterID string, nodeID string) error
	UnpauseNode(ctx context.Context, clusterID string, nodeID string) error
	RedeployCluster(ctx context.Context, clusterID string) error
	CreateCapellaLink(ctx context.Context, columnarID, linkName, clusterId, directID string) error
	CreateS3Link(ctx context.Context, columnarID, linkName, region, endpoint, accessKey, secretKey string) error
	DropLink(ctx context.Context, columnarID, linkName string) error
	EnableDataApi(ctx context.Context, clusterID string) error
}
