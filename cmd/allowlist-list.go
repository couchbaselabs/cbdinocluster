package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var allowListListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists all allowed CIDRs",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		cluster, err := helper.IdentifyCluster(ctx, cloudDeployer, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		entries, err := cloudDeployer.ListAllowListEntries(ctx, cluster.GetID())
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
	allowListCmd.AddCommand(allowListListCmd)
}
