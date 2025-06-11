package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type CollectLogsOutput []string

var collectLogsCmd = &cobra.Command{
	Use:   "collect-logs [flags] <cluster-id> <dest-path>",
	Short: "Fetches the logs from a cluster into a local path",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])
		destPath := args[1]

		logPaths, err := deployer.CollectLogs(ctx, cluster.GetID(), destPath)
		if err != nil {
			logger.Fatal("failed to collect logs", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("Collected Files:\n")
			for _, path := range logPaths {
				fmt.Printf("  %s\n",
					path)
			}
		} else {
			var out CollectLogsOutput = logPaths
			helper.OutputJson(out)
		}
	},
}

func init() {
	rootCmd.AddCommand(collectLogsCmd)
}
