package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var bucketsLoadSampleCmd = &cobra.Command{
	Use:   "load-sample",
	Short: "Loads a sample bucket",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.LoadSampleBucket(ctx, cluster.GetID(), bucketName)
		if err != nil {
			logger.Fatal("failed to load sample bucket", zap.Error(err))
		}
	},
}

func init() {
	bucketsCmd.AddCommand(bucketsLoadSampleCmd)
}
