package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/dockerdeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var appTelemetryEnableCmd = &cobra.Command{
	Use:   "enable <cluster-id>",
	Short: "Enables app telemetry",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		dockerDeployer, ok := deployer.(*dockerdeploy.Deployer)
		if !ok {
			logger.Fatal("Toggling app telemetry is only supported for docker deployer")
		}

		err := dockerDeployer.EnableAppTelemetry(ctx, cluster.GetID(), true)
		if err != nil {
			logger.Fatal("failed to enable app telemetry", zap.Error(err))
		}
	},
}

func init() {
	appTelemetryCmd.AddCommand(appTelemetryEnableCmd)
}
