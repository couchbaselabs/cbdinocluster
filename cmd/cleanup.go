package cmd

import (
	"context"
	"log"

	"github.com/spf13/cobra"
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Cleans up any expired resources",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		deployer := getDeployer(ctx)

		err := deployer.Cleanup(ctx)
		if err != nil {
			log.Fatalf("failed to cleanup clusters: %s", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(cleanupCmd)
}
