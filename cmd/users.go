package cmd

import (
	"github.com/spf13/cobra"
)

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Provides the ability to manipulate the users of a system",
	Run:   nil,
}

func init() {
	rootCmd.AddCommand(usersCmd)
}
