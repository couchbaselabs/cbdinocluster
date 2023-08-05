package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "ps"},
	Short:   "Lists all clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		clusters, err := deployer.ListClusters(ctx)
		if err != nil {
			logger.Fatal("failed to list clusters", zap.Error(err))
		}

		fmt.Printf("Clusters:\n")
		for _, cluster := range clusters {
			fmt.Printf("  %s [State: %s, Timeout: %s]\n", cluster.GetID(), cluster.GetState(), time.Until(cluster.GetExpiry()).Round(time.Second))
			for _, node := range cluster.GetNodes() {
				fmt.Printf("    %-16s  %-20s %-20s %s\n", node.GetID(), node.GetName(), node.GetIPAddress(), node.GetResourceID())
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
