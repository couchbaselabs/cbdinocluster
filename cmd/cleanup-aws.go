package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cleanupAwsCmd = &cobra.Command{
	Use:   "aws [flags]",
	Short: "Cleans up any expired resources in AWS",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		awsCreds := helper.GetAWSCredentials(ctx)
		config := helper.GetConfig(ctx)

		if config.AWS.Region == "" {
			logger.Fatal("cannot cleanup aws without aws configuration")
		}

		peCtrl := awscontrol.PrivateEndpointsController{
			Logger:      logger,
			Region:      config.AWS.Region,
			Credentials: awsCreds,
		}

		err := peCtrl.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to cleanup private endpoint resource", zap.Error(err))
		}
	},
}

func init() {
	cleanupCmd.AddCommand(cleanupAwsCmd)
}
