package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudAllowListAddCmd = &cobra.Command{
	Use:     "add",
	Aliases: []string{"create"},
	Short:   "Adds an allowed CIDRs",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		cluster, err := helper.IdentifyCloudCluster(ctx, prov, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		err = prov.AddAllowListEntry(ctx, cluster.ClusterID, args[1])
		if err != nil {
			logger.Fatal("failed to add allow list entry", zap.Error(err))
		}
	},
}

func init() {
	cloudAllowListCmd.AddCommand(cloudAllowListAddCmd)
}
