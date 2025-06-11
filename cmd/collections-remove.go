package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var collectionsRemoveCmd = &cobra.Command{
	Use:     "remove <cluster-id> <bucket-name> <scope-name> <collection-name>",
	Aliases: []string{"rm"},
	Short:   "Removes a collection",
	Args:    cobra.MinimumNArgs(4),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]
		scopeName := args[2]
		collectionName := args[3]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.DeleteCollection(ctx, cluster.GetID(), bucketName, scopeName, collectionName)
		if err != nil {
			logger.Fatal("failed to remove collection", zap.Error(err))
		}
	},
}

func init() {
	collectionsCmd.AddCommand(collectionsRemoveCmd)
}
