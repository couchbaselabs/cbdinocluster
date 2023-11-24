package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type CollectionsListOutput map[string][]string

var collectionsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists all the collections",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		scopes, err := deployer.ListCollections(ctx, cluster.GetID(), args[1])
		if err != nil {
			logger.Fatal("failed to list collections", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("Scopes:\n")
			for _, scope := range scopes {
				fmt.Printf("  %s\n",
					scope.Name)
			}

			fmt.Printf("Collections:\n")
			for _, scope := range scopes {
				for _, collection := range scope.Collections {
					fmt.Printf("  %s/%s\n",
						scope.Name, collection.Name)
				}
			}
		} else {
			out := make(CollectionsListOutput)
			for _, scope := range scopes {
				var collections []string
				for _, collection := range scope.Collections {
					collections = append(collections, collection.Name)
				}
				out[scope.Name] = collections
			}
			helper.OutputJson(out)
		}
	},
}

func init() {
	collectionsCmd.AddCommand(collectionsListCmd)
}
