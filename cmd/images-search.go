package cmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type ImagesSearchOutput []ImagesSearchOutput_Item

type ImagesSearchOutput_Item struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}

var imagesSearchCmd = &cobra.Command{
	Use:     "search",
	Aliases: []string{"find"},
	Short:   "Searches all the images available to use",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")
		limit, _ := cmd.Flags().GetInt("limit")

		deployer := helper.GetDeployer(ctx)
		images, err := deployer.SearchImages(ctx, args[0])
		if err != nil {
			logger.Fatal("failed to search images", zap.Error(err))
		}

		if !outputJson {
			// we want dockerhub images before ghcr images
			getSourceKey := func(source string) string {
				if source == "ghcr" {
					return "4"
				} else if source == "dockerhub" {
					return "6"
				}
				return "5"
			}

			// We sort backwards to put new versions at the top
			slices.SortFunc(images, func(a deployment.Image, b deployment.Image) int {
				return -strings.Compare(
					getSourceKey(a.Source)+a.Name,
					getSourceKey(b.Source)+b.Name)
			})

			fmt.Printf("Images:\n")
			for imageNum, image := range images {
				if limit > 0 && imageNum > limit {
					fmt.Printf("  ...\n")
					break
				}

				fmt.Printf("  %s [Source: %s]\n", image.Name, image.Source)
			}
		} else {
			var out ImagesSearchOutput
			for imageNum, image := range images {
				if limit > 0 && imageNum > limit {
					break
				}

				out = append(out, ImagesSearchOutput_Item{
					Source: image.Source,
					Name:   image.Name,
				})
			}
			helper.OutputJson(out)
		}
	},
}

func init() {
	imagesCmd.AddCommand(imagesSearchCmd)

	imagesSearchCmd.Flags().Int("limit", 10, "The maximum number of results to return")
}
