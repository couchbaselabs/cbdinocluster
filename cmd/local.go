package cmd

import (
	"github.com/spf13/cobra"
)

var localCmd = &cobra.Command{
	Use:   "local",
	Short: "Provides access to tools related to Couchbase On-Prem",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(localCmd)
}
