package cmd

import (
	"github.com/spf13/cobra"
)

var ingressesCmd = &cobra.Command{
	Use:   "ingresses",
	Short: "Provides access to tools related to k8s ingresses",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(ingressesCmd)
}
