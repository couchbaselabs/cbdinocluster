package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var removeCmd = &cobra.Command{
	Use:     "remove [flags] [cluster]",
	Aliases: []string{"rm"},
	Short:   "Removes a cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		cluster, err := helper.IdentifyCluster(ctx, deployer, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		err = deployer.RemoveCluster(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to remove cluster", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
