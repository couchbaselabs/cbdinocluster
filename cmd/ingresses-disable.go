package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/caodeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var ingressesDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disables ingresses",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		caoDeployer, ok := deployer.(*caodeploy.Deployer)
		if !ok {
			logger.Fatal("ingresses are only supported for cao deployer")
		}

		err := caoDeployer.DisableIngresses(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to disable ingresses", zap.Error(err))
		}
	},
}

func init() {
	ingressesCmd.AddCommand(ingressesDisableCmd)
}
