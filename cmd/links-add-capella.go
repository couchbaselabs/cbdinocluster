package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var linksCapellaCmd = &cobra.Command{
	Use:   "capella",
	Short: "Link a capella cluster to a columnar instance. Provide either a capella Cbdino id, or a capella cluster id (i.e. not created by cbdino) ",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		linkName, _ := cmd.Flags().GetString("link-name")
		capellaId, _ := cmd.Flags().GetString("cbd-id")
		directId, _ := cmd.Flags().GetString("capella-id")

		if linkName == "" {
			logger.Fatal("you must specify a link name")
		}

		if capellaId == directId && directId == "" {
			logger.Fatal("you must specify only one of a cbd-id or a direct capella-id ")
		}

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		cloudDeployer, ok := deployer.(*clouddeploy.Deployer)
		if !ok {
			logger.Fatal("links capella is only supported for cloud deployments")
		}

		err := cloudDeployer.CreateCapellaLink(ctx, cluster.GetID(), linkName, capellaId, directId)
		if err != nil {
			logger.Fatal("failed to link capella cluster", zap.Error(err))
		}
	},
}

func init() {
	linksAddCmd.AddCommand(linksCapellaCmd)

	linksCapellaCmd.Flags().String("link-name", "", "The name of the link to be created")
	linksCapellaCmd.Flags().String("cbd-id", "", "The cbdino id of the capella cluster to link")
	linksCapellaCmd.Flags().String("capella-id", "", "The direct capella cluster id, if created without cbdino")

}
