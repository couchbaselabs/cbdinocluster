package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var queryCmd = &cobra.Command{
	Use:     "query [flags] <cluster-id> <query>",
	Aliases: []string{"conn-str"},
	Short:   "Executes a query against the cluster",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		username, _ := cmd.Flags().GetString("username")
		password, _ := cmd.Flags().GetString("password")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		res, err := deployer.ExecuteQuery(ctx, cluster.GetID(), args[1], &deployment.ExecuteQueryOptions{
			Username: username,
			Password: password,
		})
		if err != nil {
			logger.Fatal("failed to execute query", zap.Error(err))
		}

		fmt.Printf("%s\n", res)
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)

	queryCmd.Flags().String("username", "", "the database user to run the query as (cloud clusters need this)")
	queryCmd.Flags().String("password", "", "the password of the database user (cloud clusters need this)")
}
