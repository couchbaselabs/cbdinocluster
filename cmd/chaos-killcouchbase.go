package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosKillCouchbaseCmd = &cobra.Command{
	Use:   "kill-couchbase <cluster-id> [<node-id-or-ip> ...]",
	Short: "Kills couchbase service on node/s present in the cluster.",
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

		err := deployer.KillCouchbase(ctx, cluster.GetID(), nodeIds)
		if err != nil {
			logger.Fatal("failed to kill couchbase", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosKillCouchbaseCmd)
}
