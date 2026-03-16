package cmd

import (
	"github.com/spf13/cobra"
)

var clusterSettingsCmd = &cobra.Command{
	Use:   "cluster-settings",
	Short: "Provides cluster settings management tools",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(clusterSettingsCmd)
}
