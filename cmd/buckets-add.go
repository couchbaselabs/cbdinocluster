package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var bucketsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Adds a new bucket",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		bucketName := args[1]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.CreateBucket(ctx, cluster.GetID(), &deployment.CreateBucketOptions{
			Name: bucketName,
		})
		if err != nil {
			logger.Fatal("failed to create bucket", zap.Error(err))
		}
	},
}

func init() {
	bucketsCmd.AddCommand(bucketsAddCmd)
}
