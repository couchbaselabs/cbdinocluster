package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/contrib/buildversion"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"rm"},
	Short:   "Gets the version of cbdinocluster",
	Run: func(cmd *cobra.Command, args []string) {
		version := buildversion.GetVersion("github.com/couchbaselabs/cbdinocluster")
		fmt.Printf("%s\n", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
