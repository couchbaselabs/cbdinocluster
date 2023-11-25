package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosBlockTrafficCmd = &cobra.Command{
	Use:   "block-traffic",
	Short: "Blocks inter-node traffic to a specific node",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		node := helper.IdentifyNode(ctx, cluster, args[1])

		err := deployer.BlockNodeTraffic(ctx, cluster.GetID(), node.GetID())
		if err != nil {
			logger.Fatal("failed to block node traffic", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosBlockTrafficCmd)
}
