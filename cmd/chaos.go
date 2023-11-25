package cmd

import (
	"github.com/spf13/cobra"
)

var chaosCmd = &cobra.Command{
	Use:   "chaos",
	Short: "Provides chaos tools for the system",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(chaosCmd)
}
