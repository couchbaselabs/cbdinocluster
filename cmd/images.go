package cmd

import (
	"github.com/spf13/cobra"
)

var imagesCmd = &cobra.Command{
	Use:   "images",
	Short: "Provides the ability to list/search server version images.",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(imagesCmd)
}
