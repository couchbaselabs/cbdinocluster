package cmd

import (
	"fmt"
	"slices"

	"github.com/couchbaselabs/cbdinocluster/utils/clustercontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var chaosPickNodeCmd = &cobra.Command{
	Use:   "pick-node <cluster-id>",
	Short: "Picks a single non-orchestrator node from the cluster for chaos testing",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterId := args[0]
		selectOrchestrator, _ := cmd.Flags().GetBool("orchestrator")

		_, _, cluster := helper.IdentifyCluster(ctx, clusterId)

		// collect only cluster nodes (exclude utility nodes like load balancers)
		type candidateNode struct {
			ID        string
			IPAddress string
		}

		var candidates []candidateNode
		for _, node := range cluster.GetNodes() {
			if !node.IsClusterNode() {
				continue
			}
			candidates = append(candidates, candidateNode{
				ID:        node.GetID(),
				IPAddress: node.GetIPAddress(),
			})
		}

		if len(candidates) == 0 {
			logger.Fatal("no cluster nodes found")
		}

		// sort for deterministic selection
		slices.SortFunc(candidates, func(a, b candidateNode) int {
			if a.ID < b.ID {
				return -1
			}
			if a.ID > b.ID {
				return 1
			}
			return 0
		})

		// query ns_server to find the orchestrator
		firstNodeIP := candidates[0].IPAddress
		ctrl := &clustercontrol.Controller{
			Logger:   logger,
			Endpoint: fmt.Sprintf("http://%s:8091", firstNodeIP),
		}

		terseInfo, err := ctrl.GetTerseClusterInfo(ctx)
		if err != nil {
			logger.Fatal("failed to get terse cluster info", zap.Error(err))
		}

		orchestratorOTP := terseInfo.Orchestrator

		// resolve orchestrator OTP to a node IP by querying each node
		var orchestratorIP string
		for _, c := range candidates {
			nodeCtrl := &clustercontrol.Controller{
				Logger:   logger,
				Endpoint: fmt.Sprintf("http://%s:8091", c.IPAddress),
			}

			localInfo, err := nodeCtrl.GetLocalInfo(ctx)
			if err != nil {
				logger.Warn("failed to get local info for node, skipping",
					zap.String("node", c.ID),
					zap.Error(err))
				continue
			}

			if localInfo.OTPNode == orchestratorOTP {
				orchestratorIP = c.IPAddress
				break
			}
		}

		if selectOrchestrator {
			// select the orchestrator specifically
			if orchestratorIP == "" {
				logger.Fatal("could not identify orchestrator node")
			}

			var orchestrator *candidateNode
			for _, c := range candidates {
				if c.IPAddress == orchestratorIP {
					foundOrchestrator := c
					orchestrator = &foundOrchestrator
					break
				}
			}

			candidates = []candidateNode{*orchestrator}
		} else {
			// exclude the orchestrator (default behavior)
			if orchestratorIP != "" {
				var filtered []candidateNode
				for _, c := range candidates {
					if c.IPAddress != orchestratorIP {
						filtered = append(filtered, c)
					}
				}
				candidates = filtered
			} else {
				logger.Warn("could not identify orchestrator node, selecting from all nodes")
			}

			if len(candidates) == 0 {
				logger.Fatal("no eligible nodes after excluding orchestrator")
			}
		}

		selected := candidates[0]
		fmt.Println(selected.ID)
	},
}

func init() {
	chaosCmd.AddCommand(chaosPickNodeCmd)

	chaosPickNodeCmd.Flags().Bool("orchestrator", false, "Select the orchestrator node instead of a non-orchestrator")
}
