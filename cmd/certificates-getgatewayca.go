package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var certificatesGetGatewayCaCmd = &cobra.Command{
	Use:   "get-gateway-ca",
	Short: "Fetches the Gateway CA certificate",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cert, err := deployer.GetGatewayCertificate(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get gateway certificate", zap.Error(err))
		}

		fmt.Printf("%s\n", cert)
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetGatewayCaCmd)
}
