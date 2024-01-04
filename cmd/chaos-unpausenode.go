package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosUnpauseNodeCmd = &cobra.Command{
	Use:   "unpause-node",
	Short: "Unpauses a particular node in the cluster.",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		node := helper.IdentifyNode(ctx, cluster, args[1])

		err := deployer.UnpauseNode(ctx, cluster.GetID(), node.GetID())
		if err != nil {
			logger.Fatal("failed to resume node", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosUnpauseNodeCmd)
}
