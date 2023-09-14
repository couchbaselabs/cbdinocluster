package localdeploy

import (
	"context"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/versionident"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Deployer struct {
	Logger *zap.Logger
}

var _ deployment.Deployer = (*Deployer)(nil)

func (d *Deployer) controller() *OsxController {
	return &OsxController{
		Logger: d.Logger,
	}
}

func (d *Deployer) ListClusters(ctx context.Context) ([]deployment.ClusterInfo, error) {
	isInstalled, err := d.controller().IsInstalled(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to check if couchbase is installed")
	}

	var out []deployment.ClusterInfo
	if isInstalled {
		out = append(out, ClusterInfo{})
	}

	return out, nil
}

func (d *Deployer) NewCluster(ctx context.Context, def *clusterdef.Cluster) (deployment.ClusterInfo, error) {
	if len(def.NodeGroups) != 1 || def.NodeGroups[0].Count != 1 {
		return nil, errors.New("local deployment only supports a single node")
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

	return nil, nil
}

func (d *Deployer) GetDefinition(ctx context.Context, clusterID string) (*clusterdef.Cluster, error) {
	return nil, errors.New("localdeploy does not support fetching the cluster definition")
}

func (d *Deployer) ModifyCluster(ctx context.Context, clusterID string, def *clusterdef.Cluster) error {
	return errors.New("localdeploy does not support cluster modification")
}

func (d *Deployer) RemoveCluster(ctx context.Context, clusterID string) error {
	if clusterID != "a" {
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
		ConnStr: "couchbase://127.0.0.1",
		Mgmt:    "http://127.0.0.1:8091",
	}, nil
}
