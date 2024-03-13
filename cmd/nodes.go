package cmd

import (
	"github.com/spf13/cobra"
)

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Provides the ability to modify specific nodes.",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(nodesCmd)
}
