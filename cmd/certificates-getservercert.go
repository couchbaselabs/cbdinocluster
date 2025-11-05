package cmd

import (
	"fmt"
	"net"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var certificatesGetServerCert = &cobra.Command{
	Use:   "get-server-cert <dns-name>",
	Short: "Fetches a server cert for a given dns name",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		rootCa, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get dino certificate authority", zap.Error(err))
		}

		cert, key, err := rootCa.MakeServerCertificate("server-cert", []net.IP{}, args)
		if err != nil {
			logger.Fatal("failed to generate server certificate", zap.Error(err))
		}

		fmt.Printf("%s\n%s\n", cert, key)
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetServerCert)
}
