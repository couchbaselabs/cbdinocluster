package cmd

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/brett19/cbdyncluster2/clustercontrol"
	"github.com/brett19/cbdyncluster2/deployment"
	"github.com/brett19/cbdyncluster2/versionident"
	"github.com/spf13/cobra"
)

var allocateCmd = &cobra.Command{
	Use:     "allocate",
	Aliases: []string{"create", "alloc"},
	Short:   "Allocates a cluster",
	Run: func(cmd *cobra.Command, args []string) {
		creator := "someone@couchbase.com"
		purpose, _ := cmd.Flags().GetString("purpose")
		numNodes, _ := cmd.Flags().GetInt("num-nodes")
		version, _ := cmd.Flags().GetString("version")
		expiry, _ := cmd.Flags().GetDuration("expiry")

		if creator == "" {
			log.Fatalf("failed to identify creator for this cluster")
		}
		if purpose == "" {
			log.Fatalf("must specify a purpose")
		}
		if numNodes <= 0 {
			log.Fatalf("must specify number of nodes to allocate")
		}
		if version == "" {
			log.Fatalf("must specify the cluster version")
		}
		if expiry < 0 {
			log.Fatalf("must specify a positive expiry time")
		}

		ctx := context.Background()
		deployer := getDeployer(ctx)

		identVersion, err := versionident.Identify(ctx, version)
		if err != nil {
			log.Fatalf("failed to identify specified version: %s\n", err)
		}

		var nodes []*deployment.NewClusterNodeOptions
		for nodeIdx := 0; nodeIdx < numNodes; nodeIdx++ {
			nodes = append(nodes, &deployment.NewClusterNodeOptions{
				Name:                fmt.Sprintf("node_%d", nodeIdx),
				Version:             identVersion.Version,
				BuildNo:             identVersion.BuildNo,
				UseCommunityEdition: identVersion.CommunityEdition,
				UseServerless:       identVersion.Serverless,
			})
		}

		cluster, err := deployer.NewCluster(ctx, &deployment.NewClusterOptions{
			Creator: creator,
			Purpose: purpose,
			Expiry:  expiry,
			Nodes:   nodes,
		})
		if err != nil {
			log.Fatalf("cluster deployment failed: %s", err)
		}

		addNodes := make([]*clustercontrol.SetupNewClusterNodeOptions, len(nodes))
		for nodeIdx, node := range cluster.Nodes {
			addNodes[nodeIdx] = &clustercontrol.SetupNewClusterNodeOptions{
				Address: node.IPAddress,

				NodeSetupOptions: clustercontrol.NodeSetupOptions{
					EnableKvService:       true,
					EnableN1qlService:     true,
					EnableIndexService:    true,
					EnableFtsService:      true,
					EnableCbasService:     false,
					EnableEventingService: false,
					EnableBackupService:   false,
				},
			}
		}

		clusterMgr := clustercontrol.ClusterManager{}
		err = clusterMgr.SetupNewCluster(ctx, &clustercontrol.SetupNewClusterOptions{
			KvMemoryQuotaMB:       256,
			IndexMemoryQuotaMB:    256,
			FtsMemoryQuotaMB:      256,
			CbasMemoryQuotaMB:     1024,
			EventingMemoryQuotaMB: 256,

			Username: "Administrator",
			Password: "password",

			Nodes: addNodes,
		})
		if err != nil {
			log.Fatalf("cluster setup failed: %s", err)
		}

		fmt.Printf("%s\n", cluster.ClusterID)
	},
}

func init() {
	rootCmd.AddCommand(allocateCmd)

	allocateCmd.Flags().String("purpose", "", "The purpose for allocating this node")
	allocateCmd.Flags().Int("num-nodes", 0, "The number of nodes to initialize")
	allocateCmd.Flags().String("version", "", "The server version to use when allocating the nodes.")
	allocateCmd.Flags().Duration("expiry", 1*time.Hour, "The time to keep this cluster allocated for")
}
