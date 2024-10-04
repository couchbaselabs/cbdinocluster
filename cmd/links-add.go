package cmd

import (
	"github.com/spf13/cobra"
)

var linksAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a link to a columnar cluster",
	Run:   nil,
}

func init() {
	linksCmd.AddCommand(linksAddCmd)
}
