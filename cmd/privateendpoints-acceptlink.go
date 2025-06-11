package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var privateEndpointsAcceptLinkCmd = &cobra.Command{
	Use:   "accept-link <cluster-id> <link-id>",
	Short: "Accepts a clusters pending private endpoint link",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		err := cloudDeployer.AcceptPrivateEndpointLink(ctx, cluster.GetID(), args[1])
		if err != nil {
			logger.Fatal("failed to accept private endpoints link", zap.Error(err))
		}
	},
}

func init() {
	privateEndpointsCmd.AddCommand(privateEndpointsAcceptLinkCmd)
}
