package cmd

import (
	"github.com/spf13/cobra"
)

var cloudAllowListCmd = &cobra.Command{
	Use:   "allow-list",
	Short: "Provides access to tools related to Couchbase Cloud allow lists",
	Run:   nil,
}

func init() {
	cloudCmd.AddCommand(cloudAllowListCmd)
}
