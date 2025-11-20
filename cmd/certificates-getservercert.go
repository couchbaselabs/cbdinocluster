package cmd

import (
	"fmt"
	"net"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var certificatesGetServerCert = &cobra.Command{
	Use:   "get-server-cert",
	Short: "Fetches a server cert configured using the flags",
	Args:  cobra.MinimumNArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		rootCa, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get dino certificate authority", zap.Error(err))
		}

		ip, _ := cmd.Flags().GetString("ip")
		dns, _ := cmd.Flags().GetString("dns")

		var ipAddrs []net.IP
		if ip != "" {
			ipAddrs = []net.IP{net.ParseIP(ip)}
		}

		var dnsNames []string
		if dns != "" {
			dnsNames = []string{dns}
		}

		cert, key, err := rootCa.MakeServerCertificate("server-cert", ipAddrs, dnsNames)
		if err != nil {
			logger.Fatal("failed to generate server certificate", zap.Error(err))
		}

		fmt.Printf("%s\n%s\n", cert, key)
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetServerCert)

	certificatesGetServerCert.Flags().String("dns", "", "The dns name for the server certificate")
	certificatesGetServerCert.Flags().String("ip", "", "The ip address for the server cetificate")
}
