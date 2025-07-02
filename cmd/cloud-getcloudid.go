package cmd

import (
	"log"

	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
)

var cloudGetCloudIdCmd = &cobra.Command{
	Use:   "get-cloud-id <cluster-id>",
	Short: "Fetches the cloud ID of a specified cluster",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]

		_, _, cluster := helper.IdentifyCluster(ctx, clusterID)

		switch cluster := cluster.(type) {
		case *clouddeploy.ClusterInfo:
			log.Printf("%s", cluster.CloudClusterID)
		default:
			logger.Fatal("fetching a cloud-id is only supported for cloud deployed clusters")
		}
	},
}

func init() {
	cloudCmd.AddCommand(cloudGetCloudIdCmd)
}
