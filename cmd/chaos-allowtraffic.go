package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosAllowTrafficCmd = &cobra.Command{
	Use:   "allow-traffic <cluster-id> [<node-id-or-ip> ...]",
	Short: "Allows all traffic to a specific node",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		nodeIdents := args[1:]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		var nodeIds []string
		for _, nodeIdent := range nodeIdents {
			node := helper.IdentifyNode(ctx, cluster, nodeIdent)
			nodeIds = append(nodeIds, node.GetID())
		}

		err := deployer.AllowNodeTraffic(ctx, cluster.GetID(), nodeIds)
		if err != nil {
			logger.Fatal("failed to allow node traffic", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosAllowTrafficCmd)
}
