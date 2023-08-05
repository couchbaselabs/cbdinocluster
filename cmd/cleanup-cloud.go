package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cleanupCloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Cleans up any expired cloud resources",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudDeployer(ctx)

		err := prov.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to cleanup clusters", zap.Error(err))
		}
	},
}

func init() {
	cleanupCmd.AddCommand(cleanupCloudCmd)
}
