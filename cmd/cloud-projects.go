package cmd

import (
	"github.com/spf13/cobra"
)

var cloudProjectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manages the Capella projects that cbdinocluster clusters go into",
	Run:   nil,
}

func init() {
	cloudCmd.AddCommand(cloudProjectsCmd)
}
