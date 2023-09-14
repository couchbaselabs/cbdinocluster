package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type deployerCluster struct {
	DeployerName string
	Info         deployment.ClusterInfo
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "ps"},
	Short:   "Lists all clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		var wg sync.WaitGroup
		clustersCh := make(chan *deployerCluster, 1024)

		deployers := helper.GetAllDeployers(ctx)
		for deployerName, deployer := range deployers {
			wg.Add(1)
			go func(deployerName string, deployer deployment.Deployer) {
				deployerClusters, err := deployer.ListClusters(ctx)
				if err != nil {
					logger.Warn("failed to list clusters", zap.Error(err))
				}

				for _, cluster := range deployerClusters {
					clustersCh <- &deployerCluster{
						DeployerName: deployerName,
						Info:         cluster,
					}
				}
				wg.Done()
			}(deployerName, deployer)
		}
		go func() {
			wg.Wait()
			close(clustersCh)
		}()

		// We read in the clusters here so that the logging of stderr and stdout
		// does not get intertwined, making it hard to read in development.
		var clusters []*deployerCluster
		for clusterInfo := range clustersCh {
			clusters = append(clusters, clusterInfo)
		}

		fmt.Printf("Clusters:\n")
		for _, clusterInfo := range clusters {
			deployerName := clusterInfo.DeployerName
			cluster := clusterInfo.Info

			fmt.Printf("  %s [State: %s, Timeout: %s, Deployer: %s]\n",
				cluster.GetID(),
				cluster.GetState(),
				time.Until(cluster.GetExpiry()).Round(time.Second),
				deployerName)
			for _, node := range cluster.GetNodes() {
				fmt.Printf("    %-16s  %-20s %-20s %s\n",
					node.GetID(),
					node.GetName(),
					node.GetIPAddress(),
					node.GetResourceID())
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
