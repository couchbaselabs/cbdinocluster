package cmd

import (
	"log"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cbdinocluster",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("failed to initialize command line parser: %s", err)
	}
}

func init() {
	// some shortcuts to make things easier to use
	rootCmd.AddCommand(localAllocateCmd)
	rootCmd.AddCommand(localCleanupCmd)
	rootCmd.AddCommand(localConnstrCmd)
	rootCmd.AddCommand(localListCmd)
	rootCmd.AddCommand(localMgmtCmd)
	rootCmd.AddCommand(localRemoveAllCmd)
	rootCmd.AddCommand(localRemoveCmd)

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Turns on verbose logging")
}
