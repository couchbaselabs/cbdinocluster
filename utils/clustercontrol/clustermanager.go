package clustercontrol

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type ClusterManager struct {
	Logger *zap.Logger
}

type SetupNewClusterNodeOptions struct {
	Address     string
	ServerGroup string
	Services    []string
}

type SetupNewClusterOptions struct {
	KvMemoryQuotaMB       int
	IndexMemoryQuotaMB    int
	FtsMemoryQuotaMB      int
	CbasMemoryQuotaMB     int
	EventingMemoryQuotaMB int

	Username string
	Password string

	Nodes []*SetupNewClusterNodeOptions
}

func (m *ClusterManager) SetupNewCluster(ctx context.Context, opts *SetupNewClusterOptions) error {
	firstNode := opts.Nodes[0]
	firstNodeAddress := firstNode.Address

	firstNodeEndpoint := fmt.Sprintf("http://%s:%d", firstNodeAddress, 8091)
	firstNodeMgr := &NodeManager{
		Endpoint: firstNodeEndpoint,
	}
	firstNodeCtrl := firstNodeMgr.Controller()

	m.Logger.Info("setting up initial cluster node",
		zap.String("endpoint", firstNodeEndpoint))

	err := firstNodeMgr.SetupOneNodeCluster(ctx, &SetupOneNodeClusterOptions{
		KvMemoryQuotaMB:       opts.KvMemoryQuotaMB,
		IndexMemoryQuotaMB:    opts.IndexMemoryQuotaMB,
		FtsMemoryQuotaMB:      opts.FtsMemoryQuotaMB,
		CbasMemoryQuotaMB:     opts.CbasMemoryQuotaMB,
		EventingMemoryQuotaMB: opts.EventingMemoryQuotaMB,

		Username: opts.Username,
		Password: opts.Password,

		Services:    firstNode.Services,
		ServerGroup: firstNode.ServerGroup,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure the first node")
	}

	if len(opts.Nodes) == 1 {
		m.Logger.Info("only a single node in the cluster, skipping add+rebalance")
	} else {
		m.Logger.Info("joining additional nodes to the cluster")

		for _, node := range opts.Nodes {
			if node.Address == firstNodeAddress {
				continue
			}

			err := firstNodeCtrl.AddNode(ctx, &AddNodeOptions{
				ServerGroup: node.ServerGroup,
				Address:     node.Address,
				Services:    node.Services,
				Username:    "",
				Password:    "",
			})
			if err != nil {
				return errors.Wrap(err, "failed to configure additional node")
			}
		}

		m.Logger.Info("initiating rebalance")

		err = firstNodeMgr.Rebalance(ctx, nil)
		if err != nil {
			return errors.Wrap(err, "failed to start rebalance")
		}

		m.Logger.Info("waiting for rebalance completion")

		err = firstNodeMgr.WaitForNoRunningTasks(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to wait for tasks to complete")
		}
	}

	m.Logger.Info("cluster setup completed")

	return nil
}
