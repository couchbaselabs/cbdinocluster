package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type GetGatewayCaOutput struct {
	Cert string `json:"cert"`
}

var certificatesGetGatewayCaCmd = &cobra.Command{
	Use:   "get-gateway-ca <cluster-id>",
	Short: "Fetches the Gateway CA certificate",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cert, err := deployer.GetGatewayCertificate(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get gateway certificate", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("%s\n", cert)
		} else {
			helper.OutputJson(GetGatewayCaOutput{
				Cert: cert,
			})
		}
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetGatewayCaCmd)
}
