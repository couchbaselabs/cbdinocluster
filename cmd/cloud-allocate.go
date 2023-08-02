package cmd

import (
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/cloudprovision"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudAllocateCmd = &cobra.Command{
	Use:     "allocate [flags] [cluster]",
	Aliases: []string{"alloc", "create"},
	Short:   "Provisions a cloud cluster",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		expiry, _ := cmd.Flags().GetDuration("expiry")

		if expiry < 0 {
			logger.Fatal("must specify a positive expiry time")
		} else if expiry == 0 {
			expiry = 1 * time.Hour
		}

		cluster, err := prov.NewCluster(ctx, &cloudprovision.NewClusterOptions{
			Expiry: expiry,
		})
		if err != nil {
			logger.Fatal("cluster deployment failed", zap.Error(err))
		}

		fmt.Printf("%s\n", cluster.ClusterID)
	},
}

func init() {
	cloudCmd.AddCommand(cloudAllocateCmd)

	cloudAllocateCmd.Flags().Duration("expiry", 1*time.Hour, "The time to keep this cluster allocated for")
}
