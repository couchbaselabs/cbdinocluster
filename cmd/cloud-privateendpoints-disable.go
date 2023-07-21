package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudPrivateEndpointsDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disables the Private Endpoints feature on a cloud cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		cluster, err := helper.IdentifyCloudCluster(ctx, prov, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		err = prov.DisablePrivateEndpoints(ctx, cluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to disable private endpoints", zap.Error(err))
		}
	},
}

func init() {
	cloudPrivateEndpointsCmd.AddCommand(cloudPrivateEndpointsDisableCmd)
}
