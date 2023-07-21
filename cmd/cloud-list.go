package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "ps"},
	Short:   "Lists all cloud clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		clusters, err := prov.ListClusters(ctx)
		if err != nil {
			logger.Fatal("failed to list clusters", zap.Error(err))
		}

		fmt.Printf("Clusters:\n")
		for _, cluster := range clusters {
			fmt.Printf("  %s [State: %s, Timeout: %s]\n", cluster.ClusterID, cluster.State, time.Until(cluster.Expiry).Round(time.Second))
		}
	},
}

func init() {
	cloudCmd.AddCommand(cloudListCmd)
}
