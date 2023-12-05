package cmd

import (
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var refreshCmd = &cobra.Command{
	Use:   "refresh [flags] <cluster> <expiry>",
	Short: "Refreshes the expiry for a cluster",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		newExpiryDura, err := time.ParseDuration(args[1])
		if err != nil {
			logger.Fatal("failed to parse expiry time", zap.Error(err))
		}

		err = deployer.UpdateClusterExpiry(
			ctx,
			cluster.GetID(),
			time.Now().Add(newExpiryDura))
		if err != nil {
			logger.Fatal("failed to remove cluster", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(refreshCmd)
}
