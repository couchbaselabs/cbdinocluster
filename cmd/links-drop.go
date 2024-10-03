package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var linksDropCmd = &cobra.Command{
	Use:   "drop",
	Short: "Drop a link on a columnar instance",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		linkName := args[1]

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("links is only supported for cloud deployments")
		}

		err := cloudDeployer.DropLink(ctx, cluster.GetID(), linkName)
		if err != nil {
			logger.Fatal("failed to drop link", zap.Error(err))
		}
	},
}

func init() {
	linksCmd.AddCommand(linksDropCmd)
}
