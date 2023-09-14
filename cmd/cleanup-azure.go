package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cleanupAzureCmd = &cobra.Command{
	Use:   "azure [flags]",
	Short: "Cleans up any expired resources in Azure",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		azureCreds := helper.GetAzureCredentials(ctx)
		config := helper.GetConfig(ctx)

		if config.Azure.Region == "" {
			logger.Fatal("cannot cleanup azure without azure configuration")
		}

		peCtrl := azurecontrol.PrivateEndpointsController{
			Logger: logger,
			Region: config.Azure.Region,
			Creds:  azureCreds,
			SubID:  config.Azure.SubID,
			RgName: config.Azure.RGName,
		}

		err := peCtrl.Cleanup(ctx)
		if err != nil {
			logger.Fatal("failed to cleanup private endpoint resource", zap.Error(err))
		}
	},
}

func init() {
	cleanupCmd.AddCommand(cleanupAzureCmd)
}
