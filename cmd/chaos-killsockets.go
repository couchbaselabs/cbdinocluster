package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosKillSocketsCmd = &cobra.Command{
	Use:   "kill-sockets <cluster-id> [<node-id-or-ip> ...]",
	Short: "Attempts to forcibly close sockets for the given service.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		nodeIdents := args[1:]

		var nodeIds []string
		for _, nodeIdent := range nodeIdents {
			node := helper.IdentifyNode(ctx, cluster, nodeIdent)
			nodeIds = append(nodeIds, node.GetID())
		}

		services, _ := cmd.Flags().GetStringArray("service")

		err := deployer.KillSockets(ctx, cluster.GetID(), nodeIds, services)
		if err != nil {
			logger.Fatal("failed to kill sockets", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosKillSocketsCmd)

	chaosKillSocketsCmd.PersistentFlags().StringArray("service", []string{"kv"}, fmt.Sprintf("Service to disrupt. Allowed services: %v",
		[]clusterdef.Service{
			clusterdef.KvService,
			clusterdef.QueryService,
			clusterdef.IndexService,
			clusterdef.SearchService,
			clusterdef.AnalyticsService,
			clusterdef.EventingService,
			clusterdef.BackupService,
		}))
}
