package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cloudinstancecontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/gcpcontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var privateEndpointsSetupLinkCmd = &cobra.Command{
	Use:   "setup-link <cluster-id>",
	Short: "Automatically configures a private link to this agent",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		shouldAutoConfig, _ := cmd.Flags().GetBool("auto")
		instanceId, _ := cmd.Flags().GetString("instance-id")
		vmId, _ := cmd.Flags().GetString("vm-id")
		instanceIdGcp, _ := cmd.Flags().GetString("gcp-instance-id")
		projectIdGcp, _ := cmd.Flags().GetString("gcp-project-id")
		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("allow-lists are only supported for cloud deployer")
		}

		cloudCluster := cluster.(*clouddeploy.ClusterInfo)

		if shouldAutoConfig {
			if instanceId != "" || vmId != "" || instanceIdGcp != "" {
				logger.Fatal("must not specify both auto and instance-id/vm-id/gcp-instance-id")
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
			case *gcpcontrol.LocalInstanceInfo:
				instanceIdGcp = selfIdentity.InstanceID
				projectIdGcp = selfIdentity.ProjectID
			default:
				logger.Fatal("unexpected self-identity type")
			}
		}

		if instanceId == "" && vmId == "" && instanceIdGcp == "" {
			logger.Fatal("must specify either auto or instance-id/vm-id/gcp-instance-id")
		}
		if (instanceId != "" && vmId != "") || (instanceIdGcp != "" && (instanceId != "" || vmId != "")) {
			logger.Fatal("must not specify multiple of instance-id,vm-id/gcp-instance-id")
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

			if !config.AWS.Enabled.Value() {
				logger.Fatal("cannot setup AWS private endpoint without AWS configuration")
			}

			peCtrl := awscontrol.PrivateEndpointsController{
				Logger:      logger,
				Region:      config.AWS.Region,
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

			if !config.Azure.Enabled.Value() {
				logger.Fatal("cannot setup Azure private endpoint without Azure configuration")
			}

			peCtrl := azurecontrol.PrivateEndpointsController{
				Logger: logger,
				Region: config.Azure.Region,
				Creds:  azureCreds,
				SubID:  config.Azure.SubID,
				RgName: config.Azure.RGName,
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
		} else if instanceIdGcp != "" {
			if !config.GCP.Enabled.Value() {
				logger.Fatal("cannot setup GCP private endpoint without GCP configuration")
			}

			if projectIdGcp == "" {
				logger.Fatal("must specify gcp-project-id when using gcp-instance-id")
			}

			gcpCreds := helper.GetGCPCredentials(ctx)

			peCtrl := gcpcontrol.PrivateEndpointsController{
				Logger:    logger,
				Zone:      config.GCP.Zone,
				Creds:     gcpCreds,
				ProjectID: projectIdGcp,
			}

			vpcInfo, err := peCtrl.GetNetworkAndSubnet(ctx, instanceIdGcp)
			if err != nil {
				logger.Fatal("failed to get network and subnet for GCP instance", zap.Error(err))
			}

			command, err := cloudDeployer.GenPrivateEndpointLinkCommand(ctx, cloudCluster.ClusterID, &capellacontrol.PrivateEndpointLinkRequest{
				VpcID:     vpcInfo.NetworkID,
				SubnetIds: vpcInfo.SubnetworkID,
			})

			err = helper.ExecuteBashCommand(command)
			if err != nil {
				logger.Fatal("failed to execute private endpoint link command", zap.Error(err))
			}

			err = cloudDeployer.AcceptPrivateEndpointLink(ctx, cloudCluster.ClusterID, projectIdGcp)
			if err != nil {
				logger.Fatal("failed to accept private endpoint link", zap.Error(err))
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
	privateEndpointsSetupLinkCmd.Flags().String("gcp-instance-id", "", "The GCP instance id to setup the link for")
	privateEndpointsSetupLinkCmd.Flags().String("gcp-project-id", "", "The GCP project id to use with the gcp-instance-id")
	privateEndpointsSetupLinkCmd.Flags().Bool("auto", false, "Attempt to identify the local instance")
}
