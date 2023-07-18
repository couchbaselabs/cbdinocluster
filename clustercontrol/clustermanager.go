package clustercontrol

import (
	"context"
	"fmt"
	"log"

	"github.com/pkg/errors"
)

type ClusterManager struct{}

type SetupNewClusterNodeOptions struct {
	Address string

	NodeSetupOptions
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

	log.Printf("setting up initial cluster node")

	err := firstNodeMgr.SetupOneNodeCluster(ctx, &SetupOneNodeClusterOptions{
		KvMemoryQuotaMB:       opts.KvMemoryQuotaMB,
		IndexMemoryQuotaMB:    opts.IndexMemoryQuotaMB,
		FtsMemoryQuotaMB:      opts.FtsMemoryQuotaMB,
		CbasMemoryQuotaMB:     opts.CbasMemoryQuotaMB,
		EventingMemoryQuotaMB: opts.EventingMemoryQuotaMB,

		Username: opts.Username,
		Password: opts.Password,

		NodeSetupOptions: firstNode.NodeSetupOptions,
	})
	if err != nil {
		return errors.Wrap(err, "failed to configure the first node")
	}

	if len(opts.Nodes) == 1 {
		log.Printf("only a single node in the cluster, skipping add+rebalance")
	} else {
		log.Printf("joining additional nodes to the cluster")

		for _, node := range opts.Nodes {
			if node.Address == firstNodeAddress {
				continue
			}

			err := firstNodeCtrl.AddNode(ctx, &AddNodeOptions{
				ServerGroup: "0",
				Address:     node.Address,
				Services:    node.NodeSetupOptions.NsServicesList(),
				Username:    "",
				Password:    "",
			})
			if err != nil {
				return errors.Wrap(err, "failed to configure additional node")
			}
		}

		log.Printf("initiating rebalance")

		err = firstNodeMgr.Rebalance(ctx)
		if err != nil {
			log.Fatalf("rebalance begin error: %s", err)
		}

		log.Printf("waiting for rebalance completion")

		err = firstNodeMgr.WaitForNoRunningTasks(ctx)
		if err != nil {
			log.Fatalf("task wait error: %s", err)
		}
	}

	log.Printf("cluster deployment completed")

	return nil
}
