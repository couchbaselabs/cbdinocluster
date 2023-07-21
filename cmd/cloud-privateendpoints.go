package cmd

import (
	"github.com/spf13/cobra"
)

var cloudPrivateEndpointsCmd = &cobra.Command{
	Use:   "private-endpoints",
	Short: "Provides access to tools related to Couchbase Cloud Private Endpoints",
	Run:   nil,
}

func init() {
	cloudCmd.AddCommand(cloudPrivateEndpointsCmd)
}
