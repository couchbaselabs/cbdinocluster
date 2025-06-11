package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosBlockTrafficCmd = &cobra.Command{
	Use:   "block-traffic <cluster-id> <node-id-or-ip> [nodes/clients/all]",
	Short: "Blocks a type of traffic to a specific node",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		node := helper.IdentifyNode(ctx, cluster, args[1])

		blockType := deployment.BlockNodeTrafficNodes
		if len(args) > 2 {
			switch args[2] {
			case "nodes":
				blockType = deployment.BlockNodeTrafficNodes
			case "clients":
				blockType = deployment.BlockNodeTrafficClients
			case "all":
				blockType = deployment.BlockNodeTrafficAll
			default:
				logger.Fatal("unexpected traffic type",
					zap.String("type", args[2]))
			}
		}

		err := deployer.BlockNodeTraffic(ctx, cluster.GetID(), node.GetID(), blockType)
		if err != nil {
			logger.Fatal("failed to block node traffic", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosBlockTrafficCmd)
}
