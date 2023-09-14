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

type Deployer interface {
	ListClusters(ctx context.Context) ([]ClusterInfo, error)
	NewCluster(ctx context.Context, def *clusterdef.Cluster) (ClusterInfo, error)
	GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error)
	ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error
	RemoveCluster(ctx context.Context, clusterID string) error
	RemoveAll(ctx context.Context) error
	Cleanup(ctx context.Context) error
	GetConnectInfo(ctx context.Context, clusterID string) (*ConnectInfo, error)
}
