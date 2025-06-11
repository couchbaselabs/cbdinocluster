package cmd

import (
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var usersRemoveCmd = &cobra.Command{
	Use:     "remove <cluster-id> <username>",
	Aliases: []string{"rm"},
	Short:   "Removes a user",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		username := args[1]

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.DeleteUser(ctx, cluster.GetID(), username)
		if err != nil {
			logger.Fatal("failed to remove user", zap.Error(err))
		}
	},
}

func init() {
	usersCmd.AddCommand(usersRemoveCmd)
}
