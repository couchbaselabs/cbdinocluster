package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/gcpcontrol"

	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

type cleanableTarget interface {
	Cleanup(ctx context.Context) error
}

const cleanupDefaultTimeout = 30 * time.Minute

var cleanupCmd = &cobra.Command{
	Use:   "cleanup [flags] [deployer-name]",
	Short: "Cleans up any expired resources for a deployer, or for every deployer",
	Long: "Cleans up any expired resources for a deployer, or for every deployer.\n\n" +
		fmt.Sprintf("Gives up after %d minutes by default. ", int(cleanupDefaultTimeout.Minutes())) +
		"The limit covers the whole run, not each deployer. Set --timeout to change this, or --timeout 0 for no limit.",
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContextWithDefaultTimeout(cleanupDefaultTimeout)
		config := helper.GetConfig(ctx)

		purpose, _ := cmd.Flags().GetString("purpose")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		scoped := purpose != "" || dryRun
		opts := deployment.CleanupOptions{
			Purpose: purpose,
			DryRun:  dryRun,
		}

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
			if cleaner == nil {
				logger.Fatal("the specified deployer is not available",
					zap.String("deployer", selectedCleaner),
					zap.Strings("available", maps.Keys(cleaners)))
			}
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

		// Keep sweeping the other deployers, then report.
		failed := false
		for _, cleanerName := range finalCleanupOrder {
			cleaner := cleaners[cleanerName]

			if scoped {
				scopedCleaner, ok := cleaner.(deployment.ScopedCleaner)
				if !ok {
					logger.Info("cleaner cannot scope or dry run, skipping it",
						zap.String("cleaner", cleanerName))
					continue
				}

				logger.Info("running scoped cleanup",
					zap.String("cleaner", cleanerName),
					zap.String("purpose", purpose),
					zap.Bool("dry-run", dryRun))

				err := scopedCleaner.CleanupScoped(ctx, opts)
				if err != nil {
					failed = true
					logger.Error("failed to cleanup resources",
						zap.String("cleaner", cleanerName), zap.Error(err))
				}
				continue
			}

			logger.Info("running cleanup",
				zap.String("cleaner", cleanerName))

			err := cleaner.Cleanup(ctx)
			if err != nil {
				failed = true
				logger.Error("failed to cleanup resources",
					zap.String("cleaner", cleanerName), zap.Error(err))
			}
		}

		if failed {
			logger.Fatal("one or more cleaners failed")
		}
	},
}

func init() {
	rootCmd.AddCommand(cleanupCmd)

	cleanupCmd.Flags().String("purpose", "", "Only clean up the expired clusters whose purpose equals this value or starts with it followed by a dash")
	cleanupCmd.Flags().Bool("dry-run", false, "Print what would be deleted and delete nothing")
}
