package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var nodesRemoveCmd = &cobra.Command{
	Use:     "remove <cluster-id> <node-id-or-ip>",
	Aliases: []string{"rm"},
	Short:   "Removes a specific node",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		node := helper.IdentifyNode(ctx, cluster, args[1])

		err := deployer.RemoveNode(ctx, cluster.GetID(), node.GetID())
		if err != nil {
			logger.Fatal("failed to remove node", zap.Error(err))
		}
	},
}

func init() {
	nodesCmd.AddCommand(nodesRemoveCmd)
}
