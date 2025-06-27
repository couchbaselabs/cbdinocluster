package cmd

import (
	"slices"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosPartitionTrafficCmd = &cobra.Command{
	Use:   "partition-traffic <cluster-id> [<node-id-or-ip> ...]",
	Short: "Partitions intra-node traffic of the cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterId := args[0]
		nodeIdents := args[1:]
		blockTypeStr, _ := cmd.Flags().GetString("reject-with")

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterId)

		// parse any nodes the user has explicitly specified
		var partitionNodeIds []string
		for _, nodeArg := range nodeIdents {
			node := helper.IdentifyNode(ctx, cluster, nodeArg)
			partitionNodeIds = append(partitionNodeIds, node.GetID())
		}

		if len(partitionNodeIds) == 0 {
			logger.Info("no nodes specified, partitioning half nodes in the cluster")

			allNodes := cluster.GetNodes()
			allNodeIds := make([]string, 0, len(allNodes))
			for _, node := range allNodes {
				if !node.IsClusterNode() {
					continue
				}

				allNodeIds = append(allNodeIds, node.GetID())
			}

			// sort the node IDs to ensure consistent partitioning
			slices.Sort(allNodeIds)

			numPartitionNodes := len(allNodeIds) / 2
			if numPartitionNodes == 0 {
				logger.Fatal("not enough nodes in the cluster to partition traffic")
			}

			partitionNodeIds = allNodeIds[:numPartitionNodes]
		}

		err := deployer.PartitionNodeTraffic(ctx, cluster.GetID(), partitionNodeIds, blockTypeStr)
		if err != nil {
			logger.Fatal("failed to partition node traffic", zap.Error(err))
		}
	},
}

func init() {
	chaosCmd.AddCommand(chaosPartitionTrafficCmd)

	chaosPartitionTrafficCmd.Flags().String("reject-with", "", "Specifies the reject-with type to use from iptables or empty for DROP")
}
