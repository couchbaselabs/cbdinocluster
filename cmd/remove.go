package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/gcpcontrol"
	"go.uber.org/zap"

	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:     "remove [flags] <cluster-id>",
	Aliases: []string{"rm"},
	Short:   "Removes a cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		err := deployer.RemoveCluster(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to remove cluster", zap.Error(err))
		}

		switch cloudCluster := cluster.(type) {
		case *clouddeploy.ClusterInfo:
			if cloudCluster.CloudClusterID != "" {
				if cloudCluster.CloudProvider == "gcp" {
					config := helper.GetConfig(ctx)
					gcpCreds := helper.GetGCPCredentials(ctx)

					peCtrl := gcpcontrol.PrivateEndpointsController{
						Logger:    logger,
						Creds:     gcpCreds,
						ProjectID: config.GCP.ProjectID,
						Region:    config.GCP.Region,
					}

					err := peCtrl.RemovePrivateDnsZone(ctx, cloudCluster.CloudClusterID[:15])
					if err != nil {
						logger.Fatal("failed to remove private DNS entries", zap.Error(err))
					}
				}
			} else {
				logger.Warn("cloud cluster id is unavailable, deployment may have failed")
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
