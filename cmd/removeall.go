package cmd

import (
	"context"
	"log"

	"github.com/spf13/cobra"
)

var removeAllCommand = &cobra.Command{
	Use:   "remove-all",
	Short: "Removes all running clusters",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		deployer := getDeployer(ctx)

		err := deployer.RemoveAll(ctx)
		if err != nil {
			log.Fatalf("failed to remove all clusters: %s", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(removeAllCommand)
}
