package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var allowListRemoveCmd = &cobra.Command{
	Use:     "remove <cluster-id> <cidr>",
	Aliases: []string{"delete"},
	Short:   "Removes an allowed CIDRs",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		err := cloudDeployer.RemoveAllowListEntry(ctx, cluster.GetID(), args[1])
		if err != nil {
			logger.Fatal("failed to remove allow list entry", zap.Error(err))
		}
	},
}

func init() {
	allowListCmd.AddCommand(allowListRemoveCmd)
}
