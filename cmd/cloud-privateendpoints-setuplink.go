package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/awscontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudPrivateEndpointsSetupLinkCmd = &cobra.Command{
	Use:   "setup-link",
	Short: "Automatically configures a private link to this agent",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)
		awsCreds := helper.GetAWSCredentials(ctx)

		shouldAutoConfig, _ := cmd.Flags().GetBool("auto")
		instanceId, _ := cmd.Flags().GetString("instance-id")

		cluster, err := helper.IdentifyCloudCluster(ctx, prov, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		if shouldAutoConfig {
			if instanceId != "" {
				logger.Fatal("must not specify both auto and instance-id")
			}

			liCtrl := awscontrol.LocalInstanceController{
				Logger: logger,
			}

			localInstance, err := liCtrl.Identify(ctx)
			if err != nil {
				logger.Fatal("failed to identify local instance", zap.Error(err))
			}

			if localInstance.Region != cluster.Region {
				logger.Fatal("local instance is not in the same region as the cluster")
			}

			instanceId = localInstance.InstanceID
		} else {
			if instanceId == "" {
				logger.Fatal("must specify either auto or instance-id")
			}
		}

		pe, err := prov.GetPrivateEndpointDetails(ctx, cluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to get private endpoint info", zap.Error(err))
		}

		logger.Info("private endpoint details",
			zap.String("service-name", pe.ServiceName),
			zap.String("private-dns", pe.PrivateDNS))

		peCtrl := awscontrol.PrivateEndpointsController{
			Logger:      logger,
			Region:      cluster.Region,
			Credentials: awsCreds,
		}

		vpceInfo, err := peCtrl.CreateVPCEndpoint(ctx, &awscontrol.CreateVPCEndpointOptions{
			ClusterID:   cluster.CloudClusterID,
			ServiceName: pe.ServiceName,
			InstanceID:  instanceId,
		})
		if err != nil {
			logger.Fatal("failed to create vpc endpoint", zap.Error(err))
		}

		err = prov.AcceptPrivateEndpointLink(ctx, cluster.ClusterID, vpceInfo.EndpointID)
		if err != nil {
			logger.Fatal("failed to accept private endpoint link", zap.Error(err))
		}

		err = peCtrl.EnableVPCEndpointPrivateDNS(ctx, vpceInfo.EndpointID)
		if err != nil {
			logger.Fatal("failed to enable private dns on link", zap.Error(err))
		}
	},
}

func init() {
	cloudPrivateEndpointsCmd.AddCommand(cloudPrivateEndpointsSetupLinkCmd)

	cloudPrivateEndpointsSetupLinkCmd.Flags().String("instance-id", "", "The instance id to setup the link for")
	cloudPrivateEndpointsSetupLinkCmd.Flags().Bool("auto", false, "Attempt to identify the local instance")
}
