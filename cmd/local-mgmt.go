package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var localMgmtCmd = &cobra.Command{
	Use:     "mgmt [flags] cluster [node]",
	Aliases: []string{"conn-str"},
	Short:   "Gets an address to management the cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)

		cluster, err := helper.IdentifyCluster(ctx, deployer, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		var specificNode *deployment.ClusterNodeInfo
		if len(args) >= 2 {
			identifiedNode, err := helper.IdentifyNode(ctx, cluster, args[1])
			if err != nil {
				logger.Fatal("failed to identify cluster", zap.Error(err))
			}

			specificNode = identifiedNode
		}

		var nodeAddr string
		for _, node := range cluster.Nodes {
			if specificNode != nil && node != specificNode {
				continue
			}

			nodeAddr = fmt.Sprintf("%s:%d", node.IPAddress, 8091)
		}

		fmt.Printf("http://%s\n", nodeAddr)
	},
}

func init() {
	localCmd.AddCommand(localMgmtCmd)
}
