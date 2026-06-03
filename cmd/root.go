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
	//
	// This relies on cobra.EnableTraverseRunHooks (set in init): by default
	// cobra runs only the most specific PersistentPreRunE, so a subcommand
	// that ever defines its own would silently shadow this one and skip the
	// --config resolution. Traversal makes every hook in the chain run.
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
	// Run persistent pre-run hooks from every command in the chain, not just
	// the most specific one. Without this, a subcommand that later defines its
	// own PersistentPreRunE would shadow the root's --config resolution above.
	cobra.EnableTraverseRunHooks = true

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Turns on verbose logging")
	rootCmd.PersistentFlags().Bool("json", false, "Turns on JSON output for supported commands")
	rootCmd.PersistentFlags().String("config", "", "Path to the config file (overrides $"+cbdcconfig.EnvConfigPath+" and the default ~/.cbdinocluster)")
}
