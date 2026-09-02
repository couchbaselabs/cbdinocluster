package cmd

import (
	"fmt"
	"sync"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
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
	Type     string                   `json:"type"`
	State    string                   `json:"state"`
	Purpose  string                   `json:"purpose,omitempty"`
	Expiry   *time.Time               `json:"expiry,omitempty"`
	Deployer string                   `json:"deployer"`
	Nodes    []ClusterListOutput_Node `json:"nodes"`

	// Capella only. Used to find the cluster in the Capella UI.
	CloudProjectID string `json:"cloud_project_id,omitempty"`
	CloudClusterID string `json:"cloud_cluster_id,omitempty"`
}

type ClusterListOutput_Node struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IPAddress     string `json:"ip_address"`
	ResourceID    string `json:"resource_id"`
	IsClusterNode bool   `json:"is_cluster_node"`
}

var listCmd = &cobra.Command{
	Use:     "list [cluster-id]",
	Aliases: []string{"ls", "ps"},
	Short:   "Lists all clusters, or only those matching an optional cluster id prefix",
	Args:    cobra.MaximumNArgs(1),
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
				var deployerClusters []deployment.ClusterInfo
				var err error
				if len(args) == 1 {
					deployerClusters, err = deployer.FindClusters(ctx, args[0])
				} else {
					deployerClusters, err = deployer.ListClusters(ctx)
				}
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

				purposeStr := ""
				if purpose := cluster.GetPurpose(); purpose != "" {
					purposeStr = fmt.Sprintf(", Purpose: %s", purpose)
				}

				fmt.Printf("  %s [Type: %s, State: %s, Timeout: %s, Deployer: %s%s]\n",
					cluster.GetID(),
					cluster.GetType(),
					cluster.GetState(),
					expiryStr,
					deployerName,
					purposeStr)
				for _, node := range cluster.GetNodes() {
					printId := node.GetID()
					if !node.IsClusterNode() {
						printId = "[UTIL] " + printId
					}

					fmt.Printf("    %-40s %-20s %-20s %s\n",
						printId,
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
					Type:     string(cluster.Info.GetType()),
					State:    cluster.Info.GetState(),
					Purpose:  cluster.Info.GetPurpose(),
					Deployer: cluster.DeployerName,
				}

				if cloudInfo, ok := cluster.Info.(*clouddeploy.ClusterInfo); ok {
					clusterItem.CloudProjectID = cloudInfo.CloudProjectID
					clusterItem.CloudClusterID = cloudInfo.CloudClusterID
				}

				expiry := cluster.Info.GetExpiry()
				if !expiry.IsZero() {
					clusterItem.Expiry = &expiry
				}

				for _, node := range cluster.Info.GetNodes() {
					clusterItem.Nodes = append(clusterItem.Nodes, ClusterListOutput_Node{
						ID:            node.GetID(),
						Name:          node.GetName(),
						IPAddress:     node.GetIPAddress(),
						ResourceID:    node.GetResourceID(),
						IsClusterNode: node.IsClusterNode(),
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
