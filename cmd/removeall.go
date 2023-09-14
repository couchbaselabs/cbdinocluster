package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var removeAllCmd = &cobra.Command{
	Use:   "remove-all [deployer-name]",
	Short: "Removes all running clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		var deployers map[string]deployment.Deployer
		if len(args) >= 1 {
			selectedDeployer := args[0]
			deployer := helper.GetDeployerByName(ctx, selectedDeployer)
			deployers = map[string]deployment.Deployer{
				selectedDeployer: deployer,
			}
		} else {
			deployers = helper.GetAllDeployers(ctx)
		}

		for deployerName, deployer := range deployers {
			logger.Info("removing all clusters",
				zap.String("deployer", deployerName))

			err := deployer.RemoveAll(ctx)
			if err != nil {
				logger.Fatal("failed to remove all clusters", zap.Error(err))
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(removeAllCmd)
}
