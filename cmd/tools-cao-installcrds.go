package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var toolsCaoInstallCrdsCmd = &cobra.Command{
	Use:   "install-crds",
	Short: "Automatically installs the cao crds.",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		caoDeployer := helper.GetCaoDeployer(ctx)

		err := caoDeployer.GetClient().InstallDefaultCrd(ctx)
		if err != nil {
			logger.Fatal("failed to install crds", zap.Error(err))
		}
	},
}

func init() {
	toolsCaoCmd.AddCommand(toolsCaoInstallCrdsCmd)
}
