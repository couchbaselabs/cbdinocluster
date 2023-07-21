package cmd

import (
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudPrivateEndpointsMgmtCmd = &cobra.Command{
	Use:   "mgmt",
	Short: "Gets the mgmt address for a clusters private endpoint",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		waitVisible, _ := cmd.Flags().GetBool("wait-visible")

		cluster, err := helper.IdentifyCloudCluster(ctx, prov, args[0])
		if err != nil {
			logger.Fatal("failed to identify cluster", zap.Error(err))
		}

		details, err := prov.GetPrivateEndpointDetails(ctx, cluster.ClusterID)
		if err != nil {
			logger.Fatal("failed to get private endpoint details", zap.Error(err))
		}

		if waitVisible {
			for {
				ips, err := net.LookupIP(details.PrivateDNS)
				if err == nil && len(ips) == 0 {
					err = errors.New("no ip addresses for hostname")
				}
				if err != nil {
					logger.Info("waiting for private dns to become accessible", zap.Error(err))
					time.Sleep(10 * time.Second)
					continue
				}

				break
			}
		}

		fmt.Printf("https://%s:18091\n", details.PrivateDNS)
	},
}

func init() {
	cloudPrivateEndpointsCmd.AddCommand(cloudPrivateEndpointsMgmtCmd)

	cloudPrivateEndpointsMgmtCmd.Flags().Bool("wait-visible", false, "Wait for the DNS to be visible to this host")
}
