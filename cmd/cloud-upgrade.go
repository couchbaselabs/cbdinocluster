package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudUpgradeCmd = &cobra.Command{
	Use:   "upgrade <cluster-id> <current-image> <new-image>",
	Short: "Upgrades an operational or columnar cluster",
	Args:  cobra.MinimumNArgs(3),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		CurrentImages := args[1]
		NewImage := args[2]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.UpgradeCluster(ctx, cluster.GetID(), CurrentImages, NewImage)
		if err != nil {
			logger.Fatal("failed to upgrade cluster", zap.Error(err))
		}
	},
}

func init() {
	cloudCmd.AddCommand(cloudUpgradeCmd)
}
