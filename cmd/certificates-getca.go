package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var certificatesGetCaCmd = &cobra.Command{
	Use:   "get-ca <cluster-id>",
	Short: "Fetches the CA certificate",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cert, err := deployer.GetCertificate(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get certificate", zap.Error(err))
		}

		fmt.Printf("%s\n", cert)
	},
}

func init() {
	certificatesCmd.AddCommand(certificatesGetCaCmd)
}
