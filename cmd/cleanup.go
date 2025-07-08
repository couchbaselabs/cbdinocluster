package cmd

import (
	"context"
	"github.com/couchbaselabs/cbdinocluster/utils/gcpcontrol"

	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

type cleanableTarget interface {
	Cleanup(ctx context.Context) error
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup [flags] [deployer-name]",
	Short: "Cleans up any expired resources for a deployer",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		cleaners := make(map[string]cleanableTarget)

		// put all the registered deployers into the cleaners list
		deployers := helper.GetAllDeployers(ctx)
		for deployerName, deployer := range deployers {
			cleaners[deployerName] = deployer
		}

		// add special AWS target for private links
		// removable if we add an actual AWS deployer
		if deployers["aws"] != nil {
			logger.Fatal("internal error, double aws cleaners")
		}
		if config.AWS.Enabled.Value() {
			awsCreds := helper.GetAWSCredentials(ctx)
			peCtrl := &awscontrol.PrivateEndpointsController{
				Logger:      logger,
				Region:      config.AWS.Region,
				Credentials: awsCreds,
			}

			cleaners["aws"] = peCtrl
		}

		// add special Azure target for private links
		// removable if we add an actual Azure deployer
		if deployers["azure"] != nil {
			logger.Fatal("internal error, double azure cleaners")
		}
		if config.Azure.Enabled.Value() {
			azureCreds := helper.GetAzureCredentials(ctx)
			peCtrl := &azurecontrol.PrivateEndpointsController{
				Logger: logger,
				Region: config.Azure.Region,
				Creds:  azureCreds,
				SubID:  config.Azure.SubID,
				RgName: config.Azure.RGName,
			}

			cleaners["azure"] = peCtrl
		}

		// add special GCP target for private links
		// removable if we add an actual GCP deployer
		if deployers["gcp"] != nil {
			logger.Fatal("internal error, double gcp cleaners")
		}
		if config.GCP.Enabled.Value() {
			gcpCreds := helper.GetGCPCredentials(ctx)
			peCtrl := &gcpcontrol.PrivateEndpointsController{
				Logger:    logger,
				Region:    config.GCP.Region,
				Creds:     gcpCreds,
				ProjectID: config.GCP.ProjectID,
			}

			cleaners["gcp"] = peCtrl
		}

		if len(args) >= 1 {
			selectedCleaner := args[0]
			cleaner := cleaners[selectedCleaner]
			cleaners = map[string]cleanableTarget{
				selectedCleaner: cleaner,
			}
		}

		// We have to enforce a cleanup order to ensure we cleanup cloud
		// before we try to clean up the private endpoints.
		cleanupOrder := []string{"docker", "cloud", "aws", "azure", "gcp"}

		// add any cleaners we didn't have in our ordering list
		for cleanerName := range cleaners {
			if !slices.Contains(cleanupOrder, cleanerName) {
				cleanupOrder = append(cleanupOrder, cleanerName)
			}
		}

		// remove any cleaners we don't have actually available
		finalCleanupOrder := []string{}
		for _, cleanerName := range cleanupOrder {
			cleaner := cleaners[cleanerName]
			if cleaner != nil {
				finalCleanupOrder = append(finalCleanupOrder, cleanerName)
			}
		}

		logger.Info("identified cleaners and order",
			zap.Strings("cleaners", finalCleanupOrder))

		for _, cleanerName := range finalCleanupOrder {
			cleaner := cleaners[cleanerName]

			logger.Info("running cleanup",
				zap.String("cleaner", cleanerName))

			err := cleaner.Cleanup(ctx)
			if err != nil {
				logger.Fatal("failed to cleanup resources", zap.Error(err))
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(cleanupCmd)
}
