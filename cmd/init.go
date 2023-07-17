package cmd

import (
	"github.com/spf13/cobra"
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Configures the client",
	Long: `Configues the client with basic information such as the
server to communicate with and the users email for ownership
tracking purposes.`,
	Run: func(cmd *cobra.Command, args []string) {

	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
