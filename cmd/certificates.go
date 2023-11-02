package cmd

import (
	"github.com/spf13/cobra"
)

var certificatesCmd = &cobra.Command{
	Use:   "certificates",
	Short: "Provides access to tools related to Couchbase Cloud certificates",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(certificatesCmd)
}
