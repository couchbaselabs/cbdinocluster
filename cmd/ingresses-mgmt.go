package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment/caodeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var ingressMgmtCmd = &cobra.Command{
	Use:   "mgmt",
	Short: "Gets the mgmt address for a clusters private endpoint",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		waitVisible, _ := cmd.Flags().GetBool("wait-visible")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		caoDeployer, ok := deployer.(*caodeploy.Deployer)
		if !ok {
			logger.Fatal("ingresses are only supported for cao deployer")
		}

		connectInfo, err := caoDeployer.GetIngressConnectInfo(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to get ingress connect info", zap.Error(err))
		}

		if waitVisible {
			cli := http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						InsecureSkipVerify: true,
					},
				},
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}

			logger.Info("waiting for ui ingress to become accessible", zap.Error(err))

			for {
				resp, err := cli.Get(connectInfo.MgmtTls)
				if err == nil {
					if resp.StatusCode == 301 {
						// expected response from the UI
					} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
						err = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
					}
				}

				if err != nil {
					logger.Info("ui ingress was not accessible", zap.Error(err))
					time.Sleep(1 * time.Second)
					continue
				}

				break
			}
		}

		fmt.Printf("%s\n", connectInfo.MgmtTls)
	},
}

func init() {
	ingressesCmd.AddCommand(ingressMgmtCmd)

	ingressMgmtCmd.Flags().Bool("wait-visible", false, "Wait for the service to be visible to this host")
}
