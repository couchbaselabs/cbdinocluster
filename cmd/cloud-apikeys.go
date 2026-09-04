package cmd

import (
	"github.com/spf13/cobra"
)

var cloudApiKeysCmd = &cobra.Command{
	Use:   "apikeys",
	Short: "Manages the cbdinocluster created Capella API key pool",
	Run:   nil,
}

func init() {
	cloudCmd.AddCommand(cloudApiKeysCmd)
}
