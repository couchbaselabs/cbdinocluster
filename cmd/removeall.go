package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var removeAllCmd = &cobra.Command{
	Use:   "remove-all",
	Short: "Removes all running clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		err := deployer.RemoveAll(ctx)
		if err != nil {
			logger.Fatal("failed to remove all clusters", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(removeAllCmd)
}
