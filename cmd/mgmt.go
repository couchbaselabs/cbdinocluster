package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var mgmtCmd = &cobra.Command{
	Use:     "mgmt [flags] cluster [node]",
	Aliases: []string{"conn-str"},
	Short:   "Gets an address to management the cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		cluster, err := helper.IdentifyCluster(ctx, deployer, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		connectInfo, err := deployer.GetConnectInfo(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get connect info", zap.Error(err))
		}

		fmt.Printf("%s\n", connectInfo.Mgmt)
	},
}

func init() {
	rootCmd.AddCommand(mgmtCmd)
}
