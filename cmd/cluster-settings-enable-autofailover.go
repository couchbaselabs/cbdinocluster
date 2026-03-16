package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var clusterSettingsEnableAutoFailoverCmd = &cobra.Command{
	Use:   "enable-autofailover <cluster-id>",
	Short: "Enables auto-failover for a cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterId := args[0]
		timeout, _ := cmd.Flags().GetInt("timeout")

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterId)

		err := deployer.SetAutoFailover(ctx, cluster.GetID(), true, timeout)
		if err != nil {
			logger.Fatal("failed to enable auto-failover", zap.Error(err))
		}
	},
}

func init() {
	clusterSettingsCmd.AddCommand(clusterSettingsEnableAutoFailoverCmd)

	clusterSettingsEnableAutoFailoverCmd.Flags().Int("timeout", 120, "Auto-failover timeout in seconds")
}
