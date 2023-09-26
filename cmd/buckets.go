package cmd

import (
	"github.com/spf13/cobra"
)

var bucketsCmd = &cobra.Command{
	Use:   "buckets",
	Short: "Provides the ability to manipulate the buckets of a system",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(bucketsCmd)
}
