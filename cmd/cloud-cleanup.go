package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Cleans up any expired resources",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		err := prov.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to cleanup clusters", zap.Error(err))
		}
	},
}

func init() {
	cloudCmd.AddCommand(cloudCleanupCmd)
}
