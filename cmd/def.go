package cmd

import (
	"github.com/spf13/cobra"
)

var defCmd = &cobra.Command{
	Use:   "def",
	Short: "Provides access to definition manipulation tools",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(defCmd)
}
