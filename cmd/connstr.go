package cmd

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/spf13/cobra"
)

var connstrCmd = &cobra.Command{
	Use:     "connstr [flags] cluster [node]",
	Aliases: []string{"conn-str"},
	Short:   "Gets a connection string to connect to the cluster",
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
