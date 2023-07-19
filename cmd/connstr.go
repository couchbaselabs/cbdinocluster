package cmd

import (
	"fmt"
	"strings"

	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var connstrCmd = &cobra.Command{
	Use:     "connstr [flags] cluster [node]",
	Aliases: []string{"conn-str"},
	Short:   "Gets a connection string to connect to the cluster",
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

		var nodeAddrs []string
		for _, node := range cluster.Nodes {
			if specificNode != nil && node != specificNode {
				continue
			}

			portToUse := 11210
			if portToUse == 11210 {
				nodeAddrs = append(nodeAddrs, node.IPAddress)
			} else {
				nodeAddrs = append(nodeAddrs, fmt.Sprintf("%s:%d", node.IPAddress, 11210))
			}
		}

		fmt.Printf("couchbase://%s\n", strings.Join(nodeAddrs, ","))
	},
}

func init() {
	rootCmd.AddCommand(connstrCmd)
}
