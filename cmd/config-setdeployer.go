package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var configSetDeployerCmd = &cobra.Command{
	Use:   "set-deployer",
	Short: "Sets the deployer to use by default",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		config.DefaultDeployer = args[0]

		err := cbdcconfig.Save(ctx, config)
		if err != nil {
			logger.Fatal("failed to save config", zap.Error(err))
		}
	},
}

func init() {
	configCmd.AddCommand(configSetDeployerCmd)
}
