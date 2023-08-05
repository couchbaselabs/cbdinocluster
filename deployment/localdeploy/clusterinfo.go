package localdeploy

import (
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
)

type ClusterNodeInfo struct {
}

var _ (deployment.ClusterNodeInfo) = (*ClusterNodeInfo)(nil)

func (i ClusterNodeInfo) GetID() string         { return "a" }
func (i ClusterNodeInfo) GetName() string       { return "" }
func (i ClusterNodeInfo) GetResourceID() string { return "" }
func (i ClusterNodeInfo) GetIPAddress() string  { return "127.0.0.1" }

type ClusterInfo struct {
}

var _ (deployment.ClusterInfo) = (*ClusterInfo)(nil)

func (i ClusterInfo) GetID() string        { return "a" }
func (i ClusterInfo) GetPurpose() string   { return "" }
func (i ClusterInfo) GetExpiry() time.Time { return time.Time{} }
func (i ClusterInfo) GetState() string     { return "ready" }
func (i ClusterInfo) GetNodes() []deployment.ClusterNodeInfo {
	return []deployment.ClusterNodeInfo{
		ClusterNodeInfo{},
	}
}
