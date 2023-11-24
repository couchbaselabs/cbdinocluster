package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var queryCmd = &cobra.Command{
	Use:     "query [flags] cluster query",
	Aliases: []string{"conn-str"},
	Short:   "Executes a query against the cluster",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		res, err := deployer.ExecuteQuery(ctx, cluster.GetID(), args[1])
		if err != nil {
			logger.Fatal("failed to execute query", zap.Error(err))
		}

		fmt.Printf("%s\n", res)
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)
}
