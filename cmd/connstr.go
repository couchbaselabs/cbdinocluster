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
		cb2Mode, _ := cmd.Flags().GetBool("couchbase2")
		dapiMode, _ := cmd.Flags().GetBool("data-api")
		analyticsMode, _ := cmd.Flags().GetBool("analytics")

		if useTLS && noTLS {
			logger.Fatal("cannot request both TLS and non-TLS")
		}

		connstrType := ""
		if cb2Mode {
			if connstrType != "" {
				logger.Fatal("cannot request both couchbase2 and other connstr types")
			}

			connstrType = "couchbase2"
		}
		if dapiMode {
			if connstrType != "" {
				logger.Fatal("cannot request both data-api and other connstr types")
			}

			connstrType = "data-api"
		}
		if analyticsMode {
			if connstrType != "" {
				logger.Fatal("cannot request both analytics and other connstr types")
			}

			connstrType = "analytics"
		}

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		connectInfo, err := deployer.GetConnectInfo(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get connect info", zap.Error(err))
		}

		var connStr string
		if connstrType == "couchbase2" {
			if noTLS {
				logger.Fatal("cannot request non-TLS for couchbase2")
			}

			connStr = connectInfo.ConnStrCb2
			if connStr == "" {
				logger.Fatal("couchbase2 endpoint is unavailable")
			}
		} else if connstrType == "data-api" {
			if noTLS {
				logger.Fatal("cannot request non-TLS for Data API")
			}

			connStr = connectInfo.DataApiConnstr

			if connStr == "" {
				logger.Fatal("data API endpoint is unavailable")
			}
		} else if connstrType == "analytics" {
			if useTLS {
				connStr = connectInfo.AnalyticsTls
				if connStr == "" {
					logger.Fatal("TLS endpoint is unavailable")
				}
			} else if noTLS {
				connStr = connectInfo.Analytics
				if connStr == "" {
					logger.Fatal("non-TLS endpoint is unavailable")
				}
			} else {
				connStr = connectInfo.Analytics
				if connStr == "" {
					connStr = connectInfo.AnalyticsTls
				}
				if connStr == "" {
					logger.Fatal("no endpoint available")
				}
			}
		} else if connstrType == "" {
			if useTLS {
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
		} else {
			logger.Fatal("unknown connstr type", zap.String("type", connstrType))
		}

		fmt.Printf("%s\n", connStr)
	},
}

func init() {
	rootCmd.AddCommand(connstrCmd)

	connstrCmd.PersistentFlags().Bool("couchbase2", false, "Requests a couchbase2 connstr")
	connstrCmd.PersistentFlags().Bool("tls", false, "Explicitly requests a TLS endpoint")
	connstrCmd.PersistentFlags().Bool("no-tls", false, "Explicitly requests non-TLS endpoint")
	connstrCmd.PersistentFlags().Bool("data-api", false, "Requests a Data API connstr")
	connstrCmd.PersistentFlags().Bool("analytics", false, "Requests an Analytics connstr")
}
