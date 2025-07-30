package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"strings"
)

var nodesSetRecoveryCmd = &cobra.Command{
	Use:   "set-recovery <cluster-id> <node-id-or-ip>",
	Short: "Set the recovery type for a node in the cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		recoveryTypeStr, _ := cmd.Flags().GetString("type")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		node := helper.IdentifyNode(ctx, cluster, args[1])

		var recoveryType deployment.RecoveryType
		switch strings.ToLower(recoveryTypeStr) {
		case "full":
			recoveryType = deployment.FullRecovery
		case "delta":
			recoveryType = deployment.DeltaRecovery
		default:
			logger.Fatal("unexpected recovery type",
				zap.String("type", recoveryTypeStr))
		}

		err := deployer.SetNodeRecovery(ctx, cluster.GetID(), node.GetID(), recoveryType)
		if err != nil {
			logger.Fatal("failed to recovery type", zap.Error(err))
		}
	},
}

func init() {
	nodesCmd.AddCommand(nodesSetRecoveryCmd)
	nodesSetRecoveryCmd.Flags().String("type", "", "the type of failover recovery [full|delta]")
}
