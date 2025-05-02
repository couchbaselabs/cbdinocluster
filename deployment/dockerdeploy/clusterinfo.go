package dockerdeploy

import (
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
)

type ClusterNodeInfo struct {
	NodeID     string
	IsNode     bool
	Name       string
	ResourceID string
	IPAddress  string
}

var _ (deployment.ClusterNodeInfo) = (*ClusterNodeInfo)(nil)

func (i ClusterNodeInfo) GetID() string         { return i.NodeID }
func (i ClusterNodeInfo) IsClusterNode() bool   { return i.IsNode }
func (i ClusterNodeInfo) GetName() string       { return i.Name }
func (i ClusterNodeInfo) GetResourceID() string { return i.ResourceID }
func (i ClusterNodeInfo) GetIPAddress() string  { return i.IPAddress }

type ClusterInfo struct {
	ClusterID string
	Type      deployment.ClusterType
	Creator   string
	Owner     string
	Purpose   string
	Expiry    time.Time
	Nodes     []deployment.ClusterNodeInfo
}

var _ (deployment.ClusterInfo) = (*ClusterInfo)(nil)

func (i ClusterInfo) GetID() string                          { return i.ClusterID }
func (i ClusterInfo) GetType() deployment.ClusterType        { return i.Type }
func (i ClusterInfo) GetPurpose() string                     { return i.Purpose }
func (i ClusterInfo) GetExpiry() time.Time                   { return i.Expiry }
func (i ClusterInfo) GetState() string                       { return "ready" }
func (i ClusterInfo) GetNodes() []deployment.ClusterNodeInfo { return i.Nodes }
