package cmd

import (
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var usersAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Adds a new user",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		clusterID := args[0]
		username := args[1]
		password, _ := cmd.Flags().GetString("password")
		canRead, _ := cmd.Flags().GetBool("can-read")
		canWrite, _ := cmd.Flags().GetBool("can-write")

		if password == "" {
			logger.Fatal("you must specify a password to use")
		}

		_, deployer, cluster := helper.IdentifyCluster(ctx, clusterID)

		err := deployer.CreateUser(ctx, cluster.GetID(), &deployment.CreateUserOptions{
			Username: username,
			Password: password,
			CanRead:  canRead,
			CanWrite: canWrite,
		})
		if err != nil {
			logger.Fatal("failed to create user", zap.Error(err))
		}
	},
}

func init() {
	usersCmd.AddCommand(usersAddCmd)

	usersAddCmd.Flags().String("password", "", "The password to assign to the user")
	usersAddCmd.Flags().Bool("can-read", true, "Whether the user can read data")
	usersAddCmd.Flags().Bool("can-write", true, "Whether the user can write data")
}
