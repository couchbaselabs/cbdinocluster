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

		ramQuotaMB, _ := cmd.Flags().GetInt("ram-quota-mb")
		flushEnabled, _ := cmd.Flags().GetBool("flush-enabled")
		numReplicas, _ := cmd.Flags().GetInt("num-replicas")

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.CreateBucket(ctx, cluster.GetID(), &deployment.CreateBucketOptions{
			Name:         bucketName,
			RamQuotaMB:   ramQuotaMB,
			FlushEnabled: flushEnabled,
			NumReplicas:  numReplicas,
		})
		if err != nil {
			logger.Fatal("failed to create bucket", zap.Error(err))
		}
	},
}

func init() {
	bucketsCmd.AddCommand(bucketsAddCmd)

	bucketsAddCmd.Flags().Int("ram-quota-mb", 0, "The amount of RAM to provide for the bucket.")
	bucketsAddCmd.Flags().Bool("flush-enabled", false, "Whether flush is enabled on the bucket.")
}
