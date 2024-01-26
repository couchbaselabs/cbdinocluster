package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var toolsCaoUninstallAdmissionCmd = &cobra.Command{
	Use:   "uninstall-admission",
	Short: "Automatically uninstalls the cao admission controller.",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		caoDeployer := helper.GetCaoDeployer(ctx)

		namespace, _ := cmd.Flags().GetString("namespace")

		err := caoDeployer.GetClient().UninstallGlobalAdmissionController(ctx, namespace)
		if err != nil {
			logger.Fatal("failed to install admission controller", zap.Error(err))
		}
	},
}

func init() {
	toolsCaoCmd.AddCommand(toolsCaoUninstallAdmissionCmd)

	toolsCaoUninstallAdmissionCmd.Flags().String("namespace", "", "Which namespace to install in.")
}
