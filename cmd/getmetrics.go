package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var getMetricsCmd = &cobra.Command{
	Use:   "get-metrics <cluster-id>",
	Short: "Gets the cluster metrics for a cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		metrics, err := deployer.GetMetrics(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get cluster metrics", zap.Error(err))
		}

		fmt.Printf("%s\n", metrics)
	},
}

func init() {
	rootCmd.AddCommand(getMetricsCmd)
}
