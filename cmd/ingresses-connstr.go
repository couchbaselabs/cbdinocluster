package cmd

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment/caodeploy"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health/grpc_health_v1"
)

var ingressesConnstrCmd = &cobra.Command{
	Use:   "connstr <cluster-id>",
	Short: "Gets the conn for a clusters ingress",
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
			parsedUrl, err := url.Parse(connectInfo.ConnStrCb2)
			if err != nil {
				logger.Fatal("failed to parse couchbase2 connection string")
			}

			grpcHost := "" + parsedUrl.Host

			logger.Info("waiting for cng ingress to become accessible", zap.Error(err))

			for {
				conn, err := grpc.Dial(grpcHost, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
					InsecureSkipVerify: true,
				})))
				if err == nil {
					health := grpc_health_v1.NewHealthClient(conn)

					resp, checkErr := health.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
					if checkErr == nil {
						if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
							checkErr = errors.New("service status was not-serving")
						}
					}

					err = checkErr
				}

				if err != nil {
					logger.Info("cng ingress was not accessible", zap.Error(err))
					time.Sleep(1 * time.Second)
					continue
				}

				break
			}
		}

		fmt.Printf("%s\n", connectInfo.ConnStrCb2)
	},
}

func init() {
	ingressesCmd.AddCommand(ingressesConnstrCmd)

	ingressesConnstrCmd.Flags().Bool("wait-visible", false, "Wait for the service to be visible to this host")
}
