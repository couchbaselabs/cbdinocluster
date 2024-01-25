package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var connstrCmd = &cobra.Command{
	Use:     "connstr [flags] cluster",
	Aliases: []string{"conn-str"},
	Short:   "Gets a connection string to connect to the cluster",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		connectInfo, err := deployer.GetConnectInfo(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get connect info", zap.Error(err))
		}
		connStr := connectInfo.ConnStr

		useTLS, _ := cmd.Flags().GetBool("tls")
		if useTLS {
			connStr = strings.Replace(connStr, "couchbase://", "couchbases://", -1)
		}

		fmt.Printf("%s\n", connStr)
	},
}

func init() {
	connstrCmd.PersistentFlags().Bool("tls", false, "Renders secure connection string")
	rootCmd.AddCommand(connstrCmd)
}
