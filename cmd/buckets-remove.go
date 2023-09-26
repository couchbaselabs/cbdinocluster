package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var bucketsRemoveCmd = &cobra.Command{
	Use:     "remove",
	Aliases: []string{"rm"},
	Short:   "Removes a bucket",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.DeleteBucket(ctx, cluster.GetID(), bucketName)
		if err != nil {
			logger.Fatal("failed to remove bucket", zap.Error(err))
		}
	},
}

func init() {
	bucketsCmd.AddCommand(bucketsRemoveCmd)
}
