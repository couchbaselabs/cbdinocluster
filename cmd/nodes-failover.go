package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"strings"
)

var nodesFailoverCmd = &cobra.Command{
	Use:   "failover <cluster-id> <node-id-or-ip>",
	Short: "Failover a node in the cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		failOverTypeStr, _ := cmd.Flags().GetString("type")
		allowUnsafe, _ := cmd.Flags().GetBool("allow-unsafe")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		node := helper.IdentifyNode(ctx, cluster, args[1])

		var failOverType deployment.FailOverType
		switch strings.ToLower(failOverTypeStr) {
		case "hard":
			failOverType = deployment.HardFailOver
		case "graceful":
			failOverType = deployment.GracefulFailOver
		default:
			logger.Fatal("unexpected fail over type",
				zap.String("type", failOverTypeStr))
		}

		err := deployer.FailOverNode(ctx, cluster.GetID(), node.GetID(), failOverType, allowUnsafe)
		if err != nil {
			logger.Fatal("failed to fail over node", zap.Error(err))
		}
	},
}

func init() {
	nodesCmd.AddCommand(nodesFailoverCmd)
	nodesFailoverCmd.Flags().String("type", "", "the type of failover [hard|graceful]")
	nodesFailoverCmd.Flags().Bool("allow-unsafe", false, "allow unsafe failover (for hard failover only)")
}
