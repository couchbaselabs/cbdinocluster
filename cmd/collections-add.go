package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var collectionsAddCmd = &cobra.Command{
	Use:   "add <cluster-id> <bucket-name> <scope-name> <collection-name>",
	Short: "Adds a new collection",
	Args:  cobra.MinimumNArgs(4),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]
		scopeName := args[2]
		collectionName := args[3]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.CreateCollection(ctx, cluster.GetID(), bucketName, scopeName, collectionName)
		if err != nil {
			logger.Fatal("failed to create collection", zap.Error(err))
		}
	},
}

func init() {
	collectionsCmd.AddCommand(collectionsAddCmd)
}
