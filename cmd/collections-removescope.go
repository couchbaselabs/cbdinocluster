package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var collectionsRemoveScopeCmd = &cobra.Command{
	Use:     "remove-scope",
	Aliases: []string{"rms"},
	Short:   "Removes a scope",
	Args:    cobra.MinimumNArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]
		scopeName := args[2]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.DeleteScope(ctx, cluster.GetID(), bucketName, scopeName)
		if err != nil {
			logger.Fatal("failed to remove scope", zap.Error(err))
		}
	},
}

func init() {
	collectionsCmd.AddCommand(collectionsRemoveScopeCmd)
}
