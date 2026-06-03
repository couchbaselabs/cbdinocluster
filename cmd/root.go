package cmd

import (
	"log"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cbdinocluster",
	Short: "provides tooling for quickly creating, modifying and destroying couchbase clusters.",
	// Resolve the --config flag before any subcommand runs and pin it as the
	// config-path override. The CBDINOCLUSTER_CONFIG env var and the default
	// are handled in cbdcconfig.ConfigPath; the flag takes precedence.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		configPath, err := cmd.Flags().GetString("config")
		if err != nil {
			return err
		}
		if configPath != "" {
			cbdcconfig.SetConfigPathOverride(configPath)
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("failed to initialize command line parser: %s", err)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Turns on verbose logging")
	rootCmd.PersistentFlags().Bool("json", false, "Turns on JSON output for supported commands")
	rootCmd.PersistentFlags().String("config", "", "Path to the config file (overrides $"+cbdcconfig.EnvConfigPath+" and the default ~/.cbdinocluster)")
}
