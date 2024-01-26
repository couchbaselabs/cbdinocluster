package cmd

import (
	"runtime"

	"github.com/couchbaselabs/cbdinocluster/utils/caocontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var toolsDownloadCaoToolsCmd = &cobra.Command{
	Use:   "download-cao-tools [install-path] [version]",
	Short: "Automatically download the CAO tools.",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		installPath := args[0]
		version := args[1]

		wantOpenShift, _ := cmd.Flags().GetBool("openshift")
		osName, _ := cmd.Flags().GetString("platform")
		archName, _ := cmd.Flags().GetString("arch")

		if osName == "" {
			osName = runtime.GOOS
		}
		if archName == "" {
			archName = runtime.GOARCH
		}

		err := caocontrol.DownloadCaoTools(ctx, logger, installPath, version, osName, archName, wantOpenShift)
		if err != nil {
			logger.Fatal("failed to download cao-tools", zap.Error(err))
		}
	},
}

func init() {
	toolsCmd.AddCommand(toolsDownloadCaoToolsCmd)

	toolsDownloadCaoToolsCmd.Flags().Bool("openshift", false, "Whether to fetch the openshift tools")
	toolsDownloadCaoToolsCmd.Flags().String("platform", "", "Which platforms tools to fetch")
	toolsDownloadCaoToolsCmd.Flags().String("arch", "", "Which architectures tools to fetch")
}
