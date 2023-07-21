package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudRemoveAllCommand = &cobra.Command{
	Use:   "remove-all",
	Short: "Removes all running clusters",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		prov := helper.GetCloudProvisioner(ctx)

		// this command is so dangerous that you must pass this key that's only
		// really visible in the code to make it work...
		if len(args) < 1 || args[0] != "astrometrics" {
			fmt.Printf("woah woah woah, you should not be using this\n")
			return
		}

		err := prov.RemoveAll(ctx)
		if err != nil {
			logger.Fatal("failed to remove all clusters", zap.Error(err))
		}
	},
}

func init() {
	cloudCmd.AddCommand(cloudRemoveAllCommand)
}
