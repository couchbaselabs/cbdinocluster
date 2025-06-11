package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var linksS3Cmd = &cobra.Command{
	Use:   "s3 <cluster-id>",
	Short: "Link a S3 bucket to a columnar instance.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		linkName, _ := cmd.Flags().GetString("link-name")
		region, _ := cmd.Flags().GetString("region")
		// Optionals
		endpoint, _ := cmd.Flags().GetString("endpoint")
		accessKey, _ := cmd.Flags().GetString("access-key")
		secretKey, _ := cmd.Flags().GetString("secret-key")

		if linkName == "" {
			logger.Fatal("you must give the link a name")
		}

		if region == "" {
			logger.Fatal("you must specify an AWS region")
		}
		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("links s3 is only supported for cloud deployments")
		}

		if accessKey == "" && secretKey == "" {
			awsCreds := helper.GetAWSCredentials(ctx)
			accessKey = awsCreds.AccessKeyID
			secretKey = awsCreds.SecretAccessKey
		}

		err := cloudDeployer.CreateS3Link(ctx, cluster.GetID(), linkName, region, endpoint, accessKey, secretKey)
		if err != nil {
			logger.Fatal("failed to setup S3 link", zap.Error(err))
		}
	},
}

func init() {
	linksAddCmd.AddCommand(linksS3Cmd)

	linksS3Cmd.Flags().String("link-name", "", "The name of the link to be created")
	linksS3Cmd.Flags().String("region", "", "The AWS region the S3 bucket is in.")
	linksS3Cmd.Flags().String("endpoint", "", "The S3 endpoint. Optional.")
	linksS3Cmd.Flags().String("access-key", "", "AWS AccessKeyId to use. Will use the cbdino config values if not flag not provided.")
	linksS3Cmd.Flags().String("secret-key", "", "AWS SecretKey to use. Will use the cbdino config values if not flag not provided.")
}
