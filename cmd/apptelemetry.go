package cmd

import (
	"github.com/spf13/cobra"
)

var appTelemetryCmd = &cobra.Command{
	Use:   "app-telemetry",
	Short: "Provides access to app telemetry settings",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(appTelemetryCmd)
}
