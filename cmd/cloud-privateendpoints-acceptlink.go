package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudPrivateEndpointsAcceptLinkCmd = &cobra.Command{
	Use:   "accept-link",
	Short: "Accepts a clusters pending private endpoint link",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		cluster, err := helper.IdentifyCloudCluster(ctx, prov, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		err = prov.AcceptPrivateEndpointLink(ctx, cluster.ClusterID, args[1])
		if err != nil {
			logger.Fatal("failed to accept private endpoints link", zap.Error(err))
		}
	},
}

func init() {
	cloudPrivateEndpointsCmd.AddCommand(cloudPrivateEndpointsAcceptLinkCmd)
}
