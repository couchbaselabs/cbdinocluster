package cmd

import (
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Provides access to configuration options for dinocluster",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(configCmd)
}
