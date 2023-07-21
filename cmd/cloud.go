package cmd

import (
	"github.com/spf13/cobra"
)

var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Provides access to tools related to Couchbase Cloud",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(cloudCmd)
}
