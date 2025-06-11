package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var mgmtCmd = &cobra.Command{
	Use:     "mgmt [flags] <cluster-id> [node-id-or-ip]",
	Aliases: []string{"conn-str"},
	Short:   "Gets an address to management the cluster",
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

		var mgmtUri string
		if useTLS && noTLS {
			logger.Fatal("cannot request both TLS and non-TLS")
		} else if useTLS {
			mgmtUri = connectInfo.MgmtTls
			if mgmtUri == "" {
				logger.Fatal("TLS endpoint is unavailable")
			}
		} else if noTLS {
			mgmtUri = connectInfo.Mgmt
			if mgmtUri == "" {
				logger.Fatal("non-TLS endpoint is unavailable")
			}
		} else {
			mgmtUri = connectInfo.Mgmt
			if mgmtUri == "" {
				mgmtUri = connectInfo.MgmtTls
			}
			if mgmtUri == "" {
				logger.Fatal("no endpoint available")
			}
		}

		fmt.Printf("%s\n", mgmtUri)
	},
}

func init() {
	rootCmd.AddCommand(mgmtCmd)

	mgmtCmd.PersistentFlags().Bool("tls", false, "Explicitly requests a TLS endpoint")
	mgmtCmd.PersistentFlags().Bool("no-tls", false, "Explicitly requests non-TLS endpoint")
}
