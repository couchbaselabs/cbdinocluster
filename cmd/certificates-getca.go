package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type GetCaOutput struct {
	Cert string `json:"cert"`
}

var certificatesGetCaCmd = &cobra.Command{
	Use:   "get-ca <cluster-id>",
	Short: "Fetches the CA certificate",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cert, err := deployer.GetCertificate(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get certificate", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("%s\n", cert)
		} else {
			helper.OutputJson(GetCaOutput{
				Cert: cert,
			})
		}
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetCaCmd)
}
