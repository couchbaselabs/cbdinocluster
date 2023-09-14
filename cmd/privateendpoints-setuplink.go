package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cloudinstancecontrol"
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

		shouldAutoConfig, _ := cmd.Flags().GetBool("auto")
		instanceId, _ := cmd.Flags().GetString("instance-id")
		vmId, _ := cmd.Flags().GetString("vm-id")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		cloudCluster := cluster.(*clouddeploy.ClusterInfo)

		if shouldAutoConfig {
			if instanceId != "" || vmId != "" {
				logger.Fatal("must not specify both auto and instance-id/vm-id")
			}

			siCtrl := cloudinstancecontrol.SelfIdentifyController{
				Logger: logger,
			}

			selfIdentity, err := siCtrl.Identify(ctx)
			if err != nil {
				logger.Fatal("failed fetch self identity", zap.Error(err))
			}

			switch selfIdentity := selfIdentity.(type) {
			case *awscontrol.LocalInstanceInfo:
				instanceId = selfIdentity.InstanceID
			case *azurecontrol.LocalVmInfo:
				vmId = selfIdentity.VmID
			default:
				logger.Fatal("unexpected self-identity type")
			}
		}

		if instanceId == "" && vmId == "" {
			logger.Fatal("must specify either auto or instance-id/vm-id")
		}
		if instanceId != "" && vmId != "" {
			logger.Fatal("must not specify multiple of instance-id,vm-id")
		}

		pe, err := cloudDeployer.GetPrivateEndpointDetails(ctx, cloudCluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to get private endpoint info", zap.Error(err))
		}

		logger.Info("private endpoint details",
			zap.String("service-name", pe.ServiceName),
			zap.String("private-dns", pe.PrivateDNS))

		if instanceId != "" {
			awsCreds := helper.GetAWSCredentials(ctx)

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

			err = peCtrl.EnableVPCEndpointPrivateDNS(ctx, &awscontrol.EnableVPCEndpointPrivateDNSOptions{
				VpceID: vpceInfo.EndpointID,
			})
			if err != nil {
				logger.Fatal("failed to enable private dns on link", zap.Error(err))
			}
		} else if vmId != "" {
			azureCreds := helper.GetAzureCredentials(ctx)

			peCtrl := azurecontrol.PrivateEndpointsController{
				Logger: logger,
				Region: cloudCluster.Region,
				Creds:  azureCreds,
			}

			peData, err := peCtrl.CreateVPCEndpoint(ctx, &azurecontrol.CreateVPCEndpointOptions{
				ClusterID:    cloudCluster.ClusterID,
				ServiceID:    pe.ServiceName,
				VmResourceID: vmId,
			})
			if err != nil {
				logger.Fatal("failed to create private endpoint", zap.Error(err))
			}

			err = cloudDeployer.AcceptPrivateEndpointLink(ctx, cloudCluster.ClusterID, peData.PeName)
			if err != nil {
				logger.Fatal("failed to accept private endpoint link", zap.Error(err))
			}

			err = peCtrl.EnableVPCEndpointPrivateDNS(ctx, &azurecontrol.EnableVPCEndpointPrivateDNSOptions{
				ClusterID:    cloudCluster.ClusterID,
				PeResourceID: peData.PeResourceID,
				DnsName:      pe.PrivateDNS,
			})
			if err != nil {
				logger.Fatal("failed to enable private dns", zap.Error(err))
			}
		} else {
			logger.Fatal("unexpectedly missing instance identifier")
		}
	},
}

func init() {
	privateEndpointsCmd.AddCommand(privateEndpointsSetupLinkCmd)

	privateEndpointsSetupLinkCmd.Flags().String("instance-id", "", "The AWS instance id to setup the link for")
	privateEndpointsSetupLinkCmd.Flags().String("vm-id", "", "The Azure virtual machine id to setup the link for")
	privateEndpointsSetupLinkCmd.Flags().Bool("auto", false, "Attempt to identify the local instance")
}
