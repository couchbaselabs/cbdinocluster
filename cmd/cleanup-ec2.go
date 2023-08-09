package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cleanupEc2Cmd = &cobra.Command{
	Use:   "ec2 [flags]",
	Short: "Cleans up any expired resources in EC2",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		awsCreds := helper.GetAWSCredentials(ctx)
		config := helper.GetConfig(ctx)

		regionFlag, _ := cmd.Flags().GetString("def")

		var region string
		if region == "" {
			if regionFlag != "" {
				region = regionFlag
			}
		}
		if region == "" {
			if config.AWS != nil {
				region = config.AWS.DefaultRegion
			}
		}

		peCtrl := awscontrol.PrivateEndpointsController{
			Logger:      logger,
			Region:      region,
			Credentials: awsCreds,
		}

		err := peCtrl.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to cleanup private endpoint resource", zap.Error(err))
		}
	},
}

func init() {
	cleanupCmd.AddCommand(cleanupEc2Cmd)

	cleanupEc2Cmd.Flags().String("region", "", "The region within EC2 to clean up.")
}
