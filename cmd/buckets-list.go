package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type BucketsListOutput []BucketsListOutput_Item

type BucketsListOutput_Item struct {
	Name string `json:"name"`
}

var bucketsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists all the buckets",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		buckets, err := deployer.ListBuckets(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to list buckets", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("Buckets:\n")
			for _, bucket := range buckets {
				fmt.Printf("  %s\n",
					bucket.Name)
			}
		} else {
			var out BucketsListOutput
			for _, bucket := range buckets {
				out = append(out, BucketsListOutput_Item{
					Name: bucket.Name,
				})
			}
			helper.OutputJson(out)
		}
	},
}

func init() {
	bucketsCmd.AddCommand(bucketsListCmd)
}
