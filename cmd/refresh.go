package cmd

import (
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var refreshCmd = &cobra.Command{
	Use:   "refresh [flags] <cluster-id> <expiry>",
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

		newExpiryTime := time.Time{}
		if newExpiryDura > 0 {
			newExpiryTime = time.Now().Add(newExpiryDura)
		}

		err = deployer.UpdateClusterExpiry(
			ctx,
			cluster.GetID(),
			newExpiryTime)
		if err != nil {
			logger.Fatal("failed to remove cluster", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(refreshCmd)
}
