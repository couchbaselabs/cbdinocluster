package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cleanupDockerCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Cleans up any expired resources",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDockerDeployer(ctx)

		err := deployer.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to cleanup clusters", zap.Error(err))
		}
	},
}

func init() {
	cleanupCmd.AddCommand(cleanupDockerCmd)
}
