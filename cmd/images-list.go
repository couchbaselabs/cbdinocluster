package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type ImagesListOutput []ImagesListOutput_Item

type ImagesListOutput_Item struct {
	Source     string `json:"source"`
	Name       string `json:"name"`
	SourcePath string `json:"source-path"`
}

var imagesListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists all the images available locally",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		deployer := helper.GetDeployer(ctx)
		images, err := deployer.ListImages(ctx)
		if err != nil {
			logger.Fatal("failed to list images", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("Images:\n")
			for _, image := range images {
				if image.SourcePath != "" {
					fmt.Printf("  %s [Source: %s, Source Path: %s]\n", image.Name, image.Source, image.SourcePath)
				} else {
					fmt.Printf("  %s [Source: %s]\n", image.Name, image.Source)
				}
			}
		} else {
			var out ImagesListOutput
			for _, image := range images {
				out = append(out, ImagesListOutput_Item{
					Source:     image.Source,
					Name:       image.Name,
					SourcePath: image.SourcePath,
				})
			}
			helper.OutputJson(out)
		}
	},
}

func init() {
	imagesCmd.AddCommand(imagesListCmd)
}
