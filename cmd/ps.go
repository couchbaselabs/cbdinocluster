package cmd

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spf13/cobra"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "Lists all clusters",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		deployer := getDeployer(ctx)

		clusters, err := deployer.ListClusters(ctx)
		if err != nil {
			log.Fatalf("failed to list clusters: %s", err)
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
	rootCmd.AddCommand(psCmd)
}
