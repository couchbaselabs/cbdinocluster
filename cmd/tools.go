package cmd

import (
	"github.com/spf13/cobra"
)

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Provides access to tools for testing various dinocluster functionality",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(toolsCmd)
}
