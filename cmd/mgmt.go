package cmd

import (
	"context"
	"fmt"
	"log"

	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/spf13/cobra"
)

var mgmtCmd = &cobra.Command{
	Use:     "mgmt [flags] cluster [node]",
	Aliases: []string{"conn-str"},
	Short:   "Gets an address to management the cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		deployer := getDeployer(ctx)

		cluster, err := identifyCluster(ctx, deployer, args[0])
		if err != nil {
			log.Fatalf("failed to identify cluster: %s", err)
		}

		var specificNode *deployment.ClusterNodeInfo
		if len(args) >= 2 {
			identifiedNode, err := identifyNode(ctx, cluster, args[1])
			if err != nil {
				log.Fatalf("failed to identify cluster: %s", err)
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
	rootCmd.AddCommand(mgmtCmd)
}
