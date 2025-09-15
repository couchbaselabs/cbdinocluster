package cmd

import (
	"github.com/spf13/cobra"
)

var jwtCmd = &cobra.Command{
	Use:   "jwt",
	Short: "Provides access to tools related to JWT tokens",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(jwtCmd)
}
