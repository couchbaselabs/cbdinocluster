package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var nodesAddCmd = &cobra.Command{
	Use:     "add <cluster-id>",
	Aliases: []string{"alloc", "allocate"},
	Short:   "Adds a new node to the cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		nodeID, err := deployer.AddNode(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to add node", zap.Error(err))
		}

		fmt.Printf("%s\n", nodeID)
	},
}

func init() {
	nodesCmd.AddCommand(nodesAddCmd)
}
