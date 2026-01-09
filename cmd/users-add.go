package cmd

import (
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var usersAddCmd = &cobra.Command{
	Use:   "add <cluster-id> <username>",
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

		opts := &deployment.CreateUserOptions{
			Username: username,
			Password: password,
			CanRead:  canRead,
			CanWrite: canWrite,
		}
		err := deployer.CreateUser(ctx, cluster.GetID(), opts)
		if err != nil {
			logger.Fatal("failed to create user", zap.Error(err))
		}

		logger.Info("checking user is ready to use")

		for {
			users, err := deployer.ListUsers(ctx, cluster.GetID())
			if err != nil {
				logger.Fatal(fmt.Sprintf("failed to wait for user to be ready: %w", err))
			}

			for _, user := range users {
				// If the target has canWrite = true but canRead = false the created
				// user will have both as true
				if user.Username == opts.Username && (user.CanRead == opts.CanRead || user.CanWrite) {
					logger.Info("user is ready", zap.Any("user", user))
					return
				}
			}

			logger.Info("waiting for user to be ready...")
			time.Sleep(time.Second)
		}
	},
}

func init() {
	usersCmd.AddCommand(usersAddCmd)

	usersAddCmd.Flags().String("password", "", "The password to assign to the user")
	usersAddCmd.Flags().Bool("can-read", true, "Whether the user can read data")
	usersAddCmd.Flags().Bool("can-write", true, "Whether the user can write data")
}
