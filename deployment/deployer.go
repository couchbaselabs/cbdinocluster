package deployment

import (
	"context"
	"time"
)

type NewClusterNodeOptions struct {
	Name                string
	Version             string
	BuildNo             int
	UseCommunityEdition bool
	UseServerless       bool
}

type NewClusterOptions struct {
	Creator string
	Purpose string
	Expiry  time.Duration
	Nodes   []*NewClusterNodeOptions
}

type ClusterNodeInfo struct {
	ResourceID string
	NodeID     string
	Name       string
	IPAddress  string
}

type ClusterInfo struct {
	ClusterID string
	Creator   string
	Owner     string
	Purpose   string
	Expiry    time.Time
	Nodes     []*ClusterNodeInfo
}

type Deployer interface {
	ListClusters(ctx context.Context) ([]*ClusterInfo, error)
	NewCluster(ctx context.Context, opts *NewClusterOptions) (*ClusterInfo, error)
	RemoveCluster(ctx context.Context, clusterID string) error
	RemoveAll(ctx context.Context) error
	Cleanup(ctx context.Context) error
}
