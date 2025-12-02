package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/dockerdeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var appTelemetryDisableCmd = &cobra.Command{
	Use:   "disable <cluster-id>",
	Short: "Disables app telemetry",
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

		err := dockerDeployer.SetAppTelemetry(ctx, cluster.GetID(), false)
		if err != nil {
			logger.Fatal("failed to disable app telemetry", zap.Error(err))
		}
	},
}

func init() {
	appTelemetryCmd.AddCommand(appTelemetryDisableCmd)
}
