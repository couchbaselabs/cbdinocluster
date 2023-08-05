package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var privateEndpointsServiceNameCmd = &cobra.Command{
	Use:   "service-name",
	Short: "Gets the service-name for a clusters private endpoint",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		cluster, err := helper.IdentifyCluster(ctx, cloudDeployer, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		details, err := cloudDeployer.GetPrivateEndpointDetails(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get private endpoint details", zap.Error(err))
		}

		fmt.Printf("%s\n", details.ServiceName)
	},
}

func init() {
	privateEndpointsCmd.AddCommand(privateEndpointsServiceNameCmd)
}
