package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
)

var ipCmd = &cobra.Command{
	Use:   "ip [flags] cluster [node]",
	Short: "Gets the IP of a node in the cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		ctx := helper.GetContext()

		_, _, cluster := helper.IdentifyCluster(ctx, args[0])

		var node deployment.ClusterNodeInfo
		if len(args) >= 2 {
			node = helper.IdentifyNode(ctx, cluster, args[1])
		} else {
			node = cluster.GetNodes()[0]
		}

		fmt.Printf("%s\n", node.GetIPAddress())
	},
}

func init() {
	rootCmd.AddCommand(ipCmd)
}
