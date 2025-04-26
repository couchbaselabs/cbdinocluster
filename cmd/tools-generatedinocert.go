package cmd

import (
	"fmt"
	"net"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var toolsGenerateDinoCertCmd = &cobra.Command{
	Use:   "generate-dino-cert [...seeds] <seed>",
	Short: "Generates a dinocert certificate for a node",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		ips, _ := cmd.Flags().GetStringSlice("ip")
		dnsNames, _ := cmd.Flags().GetStringSlice("ip")

		ca, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get root cert authority", zap.Error(err))
		}

		for argIdx := 0; argIdx < len(args)-1; argIdx++ {
			ca, err = ca.MakeIntermediaryCA(args[argIdx])
			if err != nil {
				logger.Fatal("failed to get intermediary cert authority", zap.Error(err))
			}
		}

		finalSeed := args[len(args)-1]
		if len(ips) == 0 && len(dnsNames) == 0 {
			// this is an intermediary
			cert, err := ca.MakeIntermediaryCA(finalSeed)
			if err != nil {
				logger.Fatal("failed to get generate intermediary cert", zap.Error(err))
			}

			fmt.Printf("%s\n", cert.CertPem)
		} else {
			var parsedIps []net.IP
			for _, ip := range ips {
				parsedIps = append(parsedIps, net.ParseIP(ip))
			}

			// this is an intermediary
			cert, _, err := ca.MakeServerCertificate(finalSeed, parsedIps, dnsNames)
			if err != nil {
				logger.Fatal("failed to get generate server cert", zap.Error(err))
			}

			fmt.Printf("%s\n", cert)
		}
	},
}

func init() {
	toolsCmd.AddCommand(toolsGenerateDinoCertCmd)

	toolsGenerateDinoCertCmd.Flags().String("ip", "", "IP address for certificate")
	toolsGenerateDinoCertCmd.Flags().String("dns", "", "DNS name for certificate")
}
