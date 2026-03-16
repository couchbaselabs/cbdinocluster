package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var clusterSettingsDisableAutoFailoverCmd = &cobra.Command{
	Use:   "disable-autofailover <cluster-id>",
	Short: "Disables auto-failover for a cluster",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterId := args[0]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterId)

		err := deployer.SetAutoFailover(ctx, cluster.GetID(), false, 0)
		if err != nil {
			logger.Fatal("failed to disable auto-failover", zap.Error(err))
		}
	},
}

func init() {
	clusterSettingsCmd.AddCommand(clusterSettingsDisableAutoFailoverCmd)
}
