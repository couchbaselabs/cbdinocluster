package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var getdefinitionCmd = &cobra.Command{
	Use:     "get-definition [flags] <cluster-id>",
	Aliases: []string{"get-def"},
	Short:   "Gets the cluster definition for a cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		def, err := deployer.GetDefinition(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get connect info", zap.Error(err))
		}

		defStr, err := clusterdef.Stringify(def)
		if err != nil {
			logger.Fatal("failed to get definition", zap.Error(err))
		}

		fmt.Printf("%s\n", defStr)
	},
}

func init() {
	rootCmd.AddCommand(getdefinitionCmd)
}
