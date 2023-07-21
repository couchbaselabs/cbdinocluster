package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudAllowListListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists all allowed CIDRs",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		cluster, err := helper.IdentifyCloudCluster(ctx, prov, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		entries, err := prov.ListAllowListEntries(ctx, cluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to list allow list entries", zap.Error(err))
		}

		fmt.Printf("Allow List:\n")
		for _, entry := range entries {
			fmt.Printf("  %s [ID: %s, Comment: %s]\n", entry.Cidr, entry.ID, entry.Comment)
		}
	},
}

func init() {
	cloudAllowListCmd.AddCommand(cloudAllowListListCmd)
}
