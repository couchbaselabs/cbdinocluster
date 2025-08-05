package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosStopCouchbaseCmd = &cobra.Command{
	Use:   "stop-couchbase <cluster-id> [<node-id-or-ip> ...]",
	Short: "Stop couchbase service on node/s present in the cluster.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		nodeIdents := args[1:]

		var nodeIds []string
		for _, nodeIdent := range nodeIdents {
			node := helper.IdentifyNode(ctx, cluster, nodeIdent)
			nodeIds = append(nodeIds, node.GetID())
		}

		err := deployer.StopCouchbase(ctx, cluster.GetID(), nodeIds)
		if err != nil {
			logger.Fatal("failed to stop couchbase", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosStopCouchbaseCmd)
}
