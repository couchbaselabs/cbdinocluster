package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var localListCmd = &cobra.Command{
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
			fmt.Printf("  %s [Owner: %s, Creator: %s, Timeout: %s]\n", cluster.ClusterID, cluster.Owner, cluster.Creator, time.Until(cluster.Expiry).Round(time.Second))
			for _, node := range cluster.Nodes {
				fmt.Printf("    %-16s  %-20s %-20s %s\n", node.NodeID, node.Name, node.IPAddress, node.ResourceID)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(localListCmd)
	localCmd.AddCommand(localListCmd)
}
