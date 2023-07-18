package cmd

import (
	"context"
	"log"

	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm [flags] [cluster]",
	Short: "Removes a cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		deployer := getDeployer(ctx)

		cluster, err := identifyCluster(ctx, deployer, args[0])
		if err != nil {
			log.Fatalf("failed to identify cluster: %s", err)
		}

		err = deployer.RemoveCluster(ctx, cluster.ClusterID)
		if err != nil {
			log.Fatalf("failed to remove cluster: %s", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(rmCmd)
}
