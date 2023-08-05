package cmd

import (
	"github.com/spf13/cobra"
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Provides tools to cleanup things",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(cleanupCmd)
}
