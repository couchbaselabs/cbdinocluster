package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var privateEndpointsSetupLinkCmd = &cobra.Command{
	Use:   "setup-link",
	Short: "Automatically configures a private link to this agent",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		deployer := helper.GetDeployer(ctx)
		awsCreds := helper.GetAWSCredentials(ctx)

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		shouldAutoConfig, _ := cmd.Flags().GetBool("auto")
		instanceId, _ := cmd.Flags().GetString("instance-id")

		cluster, err := helper.IdentifyCluster(ctx, cloudDeployer, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		cloudCluster := cluster.(*clouddeploy.ClusterInfo)

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

			if localInstance.Region != cloudCluster.Region {
				logger.Fatal("local instance is not in the same region as the cluster")
			}

			instanceId = localInstance.InstanceID
		} else {
			if instanceId == "" {
				logger.Fatal("must specify either auto or instance-id")
			}
		}

		pe, err := cloudDeployer.GetPrivateEndpointDetails(ctx, cloudCluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to get private endpoint info", zap.Error(err))
		}

		logger.Info("private endpoint details",
			zap.String("service-name", pe.ServiceName),
			zap.String("private-dns", pe.PrivateDNS))

		peCtrl := awscontrol.PrivateEndpointsController{
			Logger:      logger,
			Region:      cloudCluster.Region,
			Credentials: awsCreds,
		}

		vpceInfo, err := peCtrl.CreateVPCEndpoint(ctx, &awscontrol.CreateVPCEndpointOptions{
			ClusterID:   cloudCluster.CloudClusterID,
			ServiceName: pe.ServiceName,
			InstanceID:  instanceId,
		})
		if err != nil {
			logger.Fatal("failed to create vpc endpoint", zap.Error(err))
		}

		err = cloudDeployer.AcceptPrivateEndpointLink(ctx, cloudCluster.ClusterID, vpceInfo.EndpointID)
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
	privateEndpointsCmd.AddCommand(privateEndpointsSetupLinkCmd)

	privateEndpointsSetupLinkCmd.Flags().String("instance-id", "", "The instance id to setup the link for")
	privateEndpointsSetupLinkCmd.Flags().Bool("auto", false, "Attempt to identify the local instance")
}
