package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/gcpcontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var removeAllCmd = &cobra.Command{
	Use:   "remove-all [flags] [deployer-name]",
	Short: "Removes all running clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		purpose, _ := cmd.Flags().GetString("purpose")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		all, _ := cmd.Flags().GetBool("all")

		if all && purpose != "" {
			logger.Fatal("--all cannot combine with --purpose")
		}
		// A dry run is not a scope, it only changes the action.
		if !all && purpose == "" {
			logger.Fatal("remove-all needs a scope. Pass --purpose to limit the removal, " +
				"or pass --all to remove every cluster. Add --dry-run to preview, " +
				"for example --all --dry-run previews everything. " +
				"To remove only the expired clusters, use cleanup")
		}

		// Only the scoped path supports a dry run, so --all --dry-run uses it too.
		scoped := !all || dryRun
		opts := deployment.RemoveAllOptions{
			Purpose: purpose,
			DryRun:  dryRun,
		}

		var deployers map[string]deployment.Deployer
		if len(args) >= 1 {
			selectedDeployer := args[0]
			deployer := helper.GetDeployerByName(ctx, selectedDeployer)
			deployers = map[string]deployment.Deployer{
				selectedDeployer: deployer,
			}
		} else {
			deployers = helper.GetAllDeployers(ctx)
		}

		for deployerName, deployer := range deployers {
			logger.Info("removing all clusters",
				zap.String("deployer", deployerName))

			if scoped {
				scopedDeployer, ok := deployer.(deployment.ScopedRemoveAller)
				if !ok {
					logger.Warn("deployer does not support scoped remove-all, skipping it",
						zap.String("deployer", deployerName))
					continue
				}

				err := scopedDeployer.RemoveAllScoped(ctx, opts)
				if err != nil {
					logger.Fatal("failed to remove all clusters", zap.Error(err))
				}

				// The private DNS sweep below removes every entry, which a scoped
				// removal must not do. The cleanup command removes the stale entries.
				continue
			}

			err := deployer.RemoveAll(ctx)
			if err != nil {
				logger.Fatal("failed to remove all clusters", zap.Error(err))
			}

			config := helper.GetConfig(ctx)
			if deployerName == "cloud" && config.GCP.Enabled.Value() {
				gcpCreds := helper.GetGCPCredentials(ctx)

				peCtrl := gcpcontrol.PrivateEndpointsController{
					Logger:    logger,
					Creds:     gcpCreds,
					ProjectID: config.GCP.ProjectID,
					Region:    config.GCP.Region,
				}

				err = peCtrl.RemoveAll(ctx)
				if err != nil {
					logger.Fatal("failed to remove private DNS entries", zap.Error(err))
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(removeAllCmd)

	removeAllCmd.Flags().String("purpose", "", "Only remove the clusters whose purpose equals this value or starts with it followed by a dash")
	removeAllCmd.Flags().Bool("dry-run", false, "Print what would be removed and remove nothing")
	removeAllCmd.Flags().Bool("all", false, "Remove every cluster the deployers own, without any scope")
}
