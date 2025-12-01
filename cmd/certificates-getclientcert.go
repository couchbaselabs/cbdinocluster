package cmd

import (
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var certificatesGetClientCertCmd = &cobra.Command{
	Use:   "get-client-cert <username>",
	Short: "Fetches a client certificate for a specific user",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		username := args[0]
		expiresInStr, _ := cmd.Flags().GetString("expires-in")

		rootCa, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get dino certificate authority", zap.Error(err))
		}

		var expiresIn time.Duration
		if expiresInStr == "" {
			// leave the expiresIn at 0 (default)
		} else {
			expiresInDate, err := time.ParseDuration(expiresInStr)
			if err != nil {
				logger.Fatal("failed to parse expires-in duration", zap.Error(err))
			}

			expiresIn = expiresInDate
		}

		cert, key, err := rootCa.MakeClientCertificate(username, expiresIn)
		if err != nil {
			logger.Fatal("failed to generate client certificate", zap.Error(err))
		}

		fmt.Printf("%s\n%s\n", cert, key)
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetClientCertCmd)
	certificatesGetClientCertCmd.Flags().String("expires-in", "", "How long before the token expires (e.g. 24h, 30m, 10s, -1h) or '' for the default fixed period")

}
