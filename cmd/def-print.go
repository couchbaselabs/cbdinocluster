package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var defPrintCmd = &cobra.Command{
	Use:   "print [flags] <definition-tag | --def | --def-file>",
	Short: "Gets the cluster definition for a cluster",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		defStr, _ := cmd.Flags().GetString("def")
		defFile, _ := cmd.Flags().GetString("def-file")

		simpleDefStr := ""
		if len(args) >= 1 {
			simpleDefStr = args[0]
		}

		def, err := helper.FetchClusterDef(simpleDefStr, defStr, defFile)
		if err != nil {
			logger.Fatal("failed to get definition", zap.Error(err))
		}

		defStr, err = clusterdef.Stringify(def)
		if err != nil {
			logger.Fatal("failed to generate definition output", zap.Error(err))
		}

		fmt.Printf("%s\n", defStr)
	},
}

func init() {
	defCmd.AddCommand(defPrintCmd)

	defPrintCmd.Flags().String("def", "", "The cluster definition you wish to provision.")
	defPrintCmd.Flags().String("def-file", "", "The path to a file containing a cluster definition to provision.")
}
