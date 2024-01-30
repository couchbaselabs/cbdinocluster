package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var modifyCmd = &cobra.Command{
	Use:     "modify [flags] [--def | --def-file]",
	Aliases: []string{"mod", "update"},
	Short:   "Modifies an existing cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")

		var def *clusterdef.Cluster

		def, err := helper.FetchClusterDef("", defStr, defFile)
		if err != nil {
			logger.Fatal("failed to get definition", zap.Error(err))
		}

		logger.Info("updating definition", zap.Any("def", def))

		deployerName, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		if def.Deployer != "" && def.Deployer != deployerName {
			logger.Fatal("cannot update the deployer for a cluster")
		}

		err = deployer.ModifyCluster(ctx, cluster.GetID(), def)
		if err != nil {
			logger.Fatal("failed to update cluster", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(modifyCmd)

	modifyCmd.Flags().String("def", "", "The cluster definition you wish to provision.")
	modifyCmd.Flags().String("def-file", "", "The path to a file containing a cluster definition to provision.")
}
