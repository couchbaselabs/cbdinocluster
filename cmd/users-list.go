package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type UsersListOutput []UsersListOutput_Item

type UsersListOutput_Item struct {
	Username string `json:"username"`
	CanRead  bool   `json:"can_read"`
	CanWrite bool   `json:"can_write"`
}

var usersListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists all the users",
	Args:    cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")

		_, deployer, cluster := helper.IdentifyCluster(ctx, args[0])

		users, err := deployer.ListUsers(ctx, cluster.GetID())
		if err != nil {
			logger.Fatal("failed to list users", zap.Error(err))
		}

		if !outputJson {
			fmt.Printf("Users:\n")
			for _, user := range users {
				fmt.Printf("  %s [Reader: %t, Writer: %t]\n",
					user.Username,
					user.CanRead,
					user.CanWrite)
			}
		} else {
			var out UsersListOutput
			for _, user := range users {
				out = append(out, UsersListOutput_Item{
					Username: user.Username,
					CanRead:  user.CanRead,
					CanWrite: user.CanWrite,
				})
			}
			helper.OutputJson(out)
		}
	},
}

func init() {
	usersCmd.AddCommand(usersListCmd)
}
