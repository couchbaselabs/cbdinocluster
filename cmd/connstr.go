package cmd

import (
	"fmt"

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

		useTLS, _ := cmd.Flags().GetBool("tls")
		noTLS, _ := cmd.Flags().GetBool("no-tls")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		connectInfo, err := deployer.GetConnectInfo(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get connect info", zap.Error(err))
		}

		var connStr string
		if useTLS && noTLS {
			logger.Fatal("cannot request both TLS and non-TLS")
		} else if useTLS {
			connStr = connectInfo.ConnStrTls
			if connStr == "" {
				logger.Fatal("TLS endpoint is unavailable")
			}
		} else if noTLS {
			connStr = connectInfo.ConnStr
			if connStr == "" {
				logger.Fatal("non-TLS endpoint is unavailable")
			}
		} else {
			connStr = connectInfo.ConnStr
			if connStr == "" {
				connStr = connectInfo.ConnStrTls
			}
			if connStr == "" {
				logger.Fatal("no endpoint available")
			}
		}

		fmt.Printf("%s\n", connStr)
	},
}

func init() {
	rootCmd.AddCommand(connstrCmd)

	connstrCmd.PersistentFlags().Bool("tls", false, "Explicitly requests a TLS endpoint")
	connstrCmd.PersistentFlags().Bool("no-tls", false, "Explicitly requests non-TLS endpoint")
}
