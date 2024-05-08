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

type ClusterListOutput []ClusterListOutput_Item

type ClusterListOutput_Item struct {
	ID       string                   `json:"id"`
	State    string                   `json:"state"`
	Expiry   *time.Time               `json:"expiry,omitempty"`
	Deployer string                   `json:"deployer"`
	Nodes    []ClusterListOutput_Node `json:"nodes"`
}

type ClusterListOutput_Node struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IPAddress  string `json:"ip_address"`
	ResourceID string `json:"resource_id"`
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "ps"},
	Short:   "Lists all clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

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

		if !outputJson {
			fmt.Printf("Clusters:\n")
			for _, clusterInfo := range clusters {
				deployerName := clusterInfo.DeployerName
				cluster := clusterInfo.Info

				expiry := cluster.GetExpiry()
				expiryStr := "none"
				if !expiry.IsZero() {
					expiryStr = time.Until(cluster.GetExpiry()).Round(time.Second).String()
				}

				fmt.Printf("  %s [State: %s, Timeout: %s, Deployer: %s]\n",
					cluster.GetID(),
					cluster.GetState(),
					expiryStr,
					deployerName)
				for _, node := range cluster.GetNodes() {
					fmt.Printf("    %-16s  %-20s %-20s %s\n",
						node.GetID(),
						node.GetName(),
						node.GetIPAddress(),
						node.GetResourceID())
				}
			}
		} else {
			var out ClusterListOutput
			for _, cluster := range clusters {
				clusterItem := ClusterListOutput_Item{
					ID:       cluster.Info.GetID(),
					State:    cluster.Info.GetState(),
					Deployer: cluster.DeployerName,
				}

				expiry := cluster.Info.GetExpiry()
				if !expiry.IsZero() {
					clusterItem.Expiry = &expiry
				}

				for _, node := range cluster.Info.GetNodes() {
					clusterItem.Nodes = append(clusterItem.Nodes, ClusterListOutput_Node{
						ID:         node.GetID(),
						Name:       node.GetName(),
						IPAddress:  node.GetIPAddress(),
						ResourceID: node.GetResourceID(),
					})
				}
				out = append(out, clusterItem)
			}
			helper.OutputJson(out)
		}
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
