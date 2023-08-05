package cmd

import (
	"github.com/spf13/cobra"
)

var privateEndpointsCmd = &cobra.Command{
	Use:   "private-endpoints",
	Short: "Provides access to tools related to Couchbase Cloud Private Endpoints",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(privateEndpointsCmd)
}
