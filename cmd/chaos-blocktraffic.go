package cmd

import (
	"slices"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosBlockTrafficCmd = &cobra.Command{
	Use:   "block-traffic <cluster-id> [<node-id-or-ip> ...]",
	Short: "Blocks a type of traffic to a specific node",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterId := args[0]
		nodeIdents := args[1:]
		trafficTypeStr, _ := cmd.Flags().GetString("from")
		blockTypeStr, _ := cmd.Flags().GetString("reject-with")

		// Backwards compatibility for single node blocking like:
		// block-traffic <cluster-id> <node-id-or-ip> [nodes/clients/all]
		if len(args) == 3 {
			if slices.Contains([]string{"nodes", "clients", "all"}, args[2]) {
				nodeIdents = []string{args[1]}
				trafficTypeStr = args[2]
			}
		}

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterId)

		var nodeIds []string
		for _, nodeIdent := range nodeIdents {
			node := helper.IdentifyNode(ctx, cluster, nodeIdent)
			nodeIds = append(nodeIds, node.GetID())
		}

		var trafficType deployment.BlockNodeTrafficType
		switch trafficTypeStr {
		case "nodes":
			trafficType = deployment.BlockNodeTrafficNodes
		case "clients":
			trafficType = deployment.BlockNodeTrafficClients
		case "all":
			trafficType = deployment.BlockNodeTrafficAll
		default:
			logger.Fatal("unexpected traffic type",
				zap.String("type", args[2]))
		}

		err := deployer.BlockNodeTraffic(ctx, cluster.GetID(), nodeIds, trafficType, blockTypeStr)
		if err != nil {
			logger.Fatal("failed to block node traffic", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosBlockTrafficCmd)

	chaosBlockTrafficCmd.Flags().String("from", "nodes", "Specifies the type of traffic to block (nodes, clients, all)")
	chaosBlockTrafficCmd.Flags().String("reject-with", "", "Specifies the reject-with type to use from iptables or empty for DROP")
}
