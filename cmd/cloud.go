package cmd

import (
	"github.com/spf13/cobra"
)

var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Provides cloud tools for the system",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(cloudCmd)
}
