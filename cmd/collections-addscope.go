package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var collectionsAddScopeCmd = &cobra.Command{
	Use:   "add-scope <cluster-id> <bucket-name> <scope-name>",
	Short: "Adds a new scope",
	Args:  cobra.MinimumNArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]
		scopeName := args[2]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.CreateScope(ctx, cluster.GetID(), bucketName, scopeName)
		if err != nil {
			logger.Fatal("failed to create scope", zap.Error(err))
		}
	},
}

func init() {
	collectionsCmd.AddCommand(collectionsAddScopeCmd)
}
