package cmd

import (
	"github.com/spf13/cobra"
)

var linksCmd = &cobra.Command{
	Use:   "links",
	Short: "Provides ability to link data sources to columnar instances",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(linksCmd)
}
