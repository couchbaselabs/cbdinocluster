package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type GetDinoCaOutput struct {
	Cert string `json:"cert"`
}

var certificatesGetDinoCaCmd = &cobra.Command{
	Use:   "get-dino-ca",
	Short: "Fetches the DinoCert CA certificate",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		outputJson, _ := cmd.Flags().GetBool("json")

		rootCa, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get dino certificate", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("%s\n", rootCa.CertPem)
		} else {
			helper.OutputJson(GetDinoCaOutput{
				Cert: string(rootCa.CertPem),
			})
		}
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetDinoCaCmd)
}
