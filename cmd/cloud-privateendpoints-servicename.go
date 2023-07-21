package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudPrivateEndpointsServiceNameCmd = &cobra.Command{
	Use:   "service-name",
	Short: "Gets the service-name for a clusters private endpoint",
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

		details, err := prov.GetPrivateEndpointDetails(ctx, cluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to get private endpoint details", zap.Error(err))
		}

		fmt.Printf("%s\n", details.ServiceName)
	},
}

func init() {
	cloudPrivateEndpointsCmd.AddCommand(cloudPrivateEndpointsServiceNameCmd)
}
