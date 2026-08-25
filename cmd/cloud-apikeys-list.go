package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudApiKeysListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "Lists the Capella API keys of the pool",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		session, err := newCapellaPoolSession(config, helper.GetInternalLogger())
		if err != nil {
			logger.Fatal("cannot access the capella api key pool", zap.Error(err))
		}

		remoteKeys, err := session.Client.ListApiKeys(ctx, session.OrgID)
		if err != nil {
			logger.Fatal("failed to list the capella api keys", zap.Error(err))
		}

		heldSecrets := make(map[string]bool, len(config.Capella.ApiKeys))
		for _, savedKey := range config.Capella.ApiKeys {
			if savedKey.Key != "" && savedKey.Secret != "" {
				heldSecrets[savedKey.Key] = true
			}
		}

		fmt.Printf("API Keys (pool: %s):\n", session.PoolName)
		for _, remoteKey := range remoteKeys {
			isPrimary := remoteKey.ID == session.PrimaryKeyID
			if !isPrimary && !strings.HasPrefix(remoteKey.Name, session.Prefix) {
				continue
			}

			marker := ""
			if isPrimary {
				marker = " [primary]"
			}

			fmt.Printf("  %s (%s) [HasSecret: %t]%s\n",
				remoteKey.ID, remoteKey.Name, heldSecrets[remoteKey.ID], marker)
		}
	},
}

func init() {
	cloudApiKeysCmd.AddCommand(cloudApiKeysListCmd)
}
