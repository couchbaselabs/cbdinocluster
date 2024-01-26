package cmd

import (
	"github.com/spf13/cobra"
)

var toolsCaoCmd = &cobra.Command{
	Use:   "cao",
	Short: "Provides access to tools for CAO",
	Run:   nil,
}

func init() {
	toolsCmd.AddCommand(toolsCaoCmd)
}
