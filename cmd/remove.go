package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var removeCmd = &cobra.Command{
	Use:     "remove [flags] <cluster-id>",
	Aliases: []string{"rm"},
	Short:   "Removes a cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		err := deployer.RemoveCluster(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to remove cluster", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
