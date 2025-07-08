package clouddeploy

import (
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
)

type ClusterInfo struct {
	ClusterID      string
	Type           deployment.ClusterType
	CloudProjectID string
	CloudClusterID string
	CloudProvider  string
	Region         string
	Expiry         time.Time
	State          string
}

var _ (deployment.ClusterInfo) = (*ClusterInfo)(nil)

func (i ClusterInfo) GetID() string                   { return i.ClusterID }
func (i ClusterInfo) GetType() deployment.ClusterType { return i.Type }
func (i ClusterInfo) GetPurpose() string              { return "" }
func (i ClusterInfo) GetExpiry() time.Time            { return i.Expiry }
func (i ClusterInfo) GetState() string                { return i.State }
func (i ClusterInfo) GetNodes() []deployment.ClusterNodeInfo {
	return nil
}
