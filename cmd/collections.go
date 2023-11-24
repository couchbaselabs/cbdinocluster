package cmd

import (
	"github.com/spf13/cobra"
)

var collectionsCmd = &cobra.Command{
	Use:   "collections",
	Short: "Provides the ability to manipulate the collections of a system",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(collectionsCmd)
}
