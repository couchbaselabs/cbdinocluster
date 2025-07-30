package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var rebalanceCmd = &cobra.Command{
	Use:   "rebalance <cluster-id> [<node-id-or-ip-to-eject> ...]",
	Short: "Rebalance the cluster, ejecting any specified nodes",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		nodesToEject := args[1:]
		var nodeIds []string
		for _, nodeIdent := range nodesToEject {
			node := helper.IdentifyNode(ctx, cluster, nodeIdent)
			nodeIds = append(nodeIds, node.GetID())
		}

		err := deployer.RebalanceCluster(ctx, cluster.GetID(), nodeIds)
		if err != nil {
			logger.Fatal("failed to rebalance cluster", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(rebalanceCmd)
}
