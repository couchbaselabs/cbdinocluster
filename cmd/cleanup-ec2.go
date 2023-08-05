package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cleanupEc2Cmd = &cobra.Command{
	Use:   "ec2 [flags] <region>",
	Short: "Cleans up any expired resources in EC2",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		awsCreds := helper.GetAWSCredentials(ctx)

		region := args[0]

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
}
