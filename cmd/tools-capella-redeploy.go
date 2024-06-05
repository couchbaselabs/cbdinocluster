package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var capellaRedeployCmd = &cobra.Command{
	Use:   "capella-redeploy [flags] <cluster>",
	Short: "Redeploy the capella cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		var err = deployer.RedeployCluster(
			ctx,
			cluster.GetID())
		if err != nil {
			logger.Fatal("failed to redeploy capella cluster", zap.Error(err))
		}
	},
}

func init() {
	toolsCmd.AddCommand(capellaRedeployCmd)
}
