package cmd

import (
	"fmt"

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

		rootCa, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get dino certificate authority", zap.Error(err))
		}

		cert, key, err := rootCa.MakeClientCertificate(args[0])
		if err != nil {
			logger.Fatal("failed to generate client certificate", zap.Error(err))
		}

		fmt.Printf("%s\n%s\n", cert, key)
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetClientCertCmd)
}
