package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudApiKeysRemoveCmd = &cobra.Command{
	Use:     "remove [key-id ...]",
	Aliases: []string{"delete"},
	Short:   "Removes the pool Capella API keys from Capella",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		dryRun, _ := cmd.Flags().GetBool("dry-run")

		session, err := newCapellaPoolSession(config, helper.GetInternalLogger())
		if err != nil {
			logger.Fatal("cannot access the capella api key pool", zap.Error(err))
		}

		remoteKeys, err := session.Client.ListApiKeys(ctx, session.OrgID)
		if err != nil {
			logger.Fatal("failed to list the capella api keys", zap.Error(err))
		}

		// Only a saved secret Capella still lists can serve a request, so the
		// deletions spread over the pool instead of loading the primary key.
		poolKeys := partitionCapellaPoolKeys(session.Prefix, session.PrimaryKeyID,
			config.Capella.ApiKeys, remoteKeys)
		for _, matchedKey := range poolKeys.Matched {
			session.Client.AddSecretKey(matchedKey.Secret)
		}

		savedSecrets := make(map[string]string, len(config.Capella.ApiKeys))
		for _, savedKey := range config.Capella.ApiKeys {
			savedSecrets[savedKey.Key] = savedKey.Secret
		}

		targetKeys, err := selectCapellaPoolRemoteKeys(session.Prefix, session.PrimaryKeyID,
			remoteKeys, args)
		if err != nil {
			logger.Fatal("failed to select the capella api keys to remove", zap.Error(err))
		}
		if len(targetKeys) == 0 {
			fmt.Printf("There are no pool Capella API keys to remove.\n")
			return
		}

		failed := false
		removedKeys := make(map[string]bool, len(targetKeys))
		for _, targetKey := range targetKeys {
			err := checkCapellaPoolKeyTarget(session.Prefix, session.PrimaryKeyID,
				targetKey.ID, targetKey.Name)
			if err != nil {
				logger.Fatal("refused to remove a capella api key", zap.Error(err))
			}

			if dryRun {
				fmt.Printf("Would remove the Capella API key %s (%s).\n",
					targetKey.ID, targetKey.Name)
				continue
			}

			// A deleted key cannot authorize anything, so its secret leaves the
			// ring before the request that kills it.
			session.Client.RemoveSecretKey(savedSecrets[targetKey.ID])

			err = session.Client.DeleteApiKey(ctx, session.OrgID, targetKey.ID)
			if err != nil {
				failed = true
				logger.Error("failed to remove a capella api key",
					zap.String("keyId", targetKey.ID), zap.Error(err))
				continue
			}

			removedKeys[targetKey.ID] = true
			fmt.Printf("Removed the Capella API key %s (%s).\n", targetKey.ID, targetKey.Name)
		}

		if len(removedKeys) > 0 {
			keptKeys := make([]cbdcconfig.Config_CapellaApiKey, 0, len(config.Capella.ApiKeys))
			for _, savedKey := range config.Capella.ApiKeys {
				if removedKeys[savedKey.Key] {
					continue
				}
				keptKeys = append(keptKeys, savedKey)
			}
			config.Capella.ApiKeys = keptKeys

			err := cbdcconfig.Save(ctx, config)
			if err != nil {
				logger.Fatal("failed to save the config", zap.Error(err))
			}
		}

		if failed {
			logger.Fatal("one or more capella api keys could not be removed")
		}
	},
}

func init() {
	cloudApiKeysCmd.AddCommand(cloudApiKeysRemoveCmd)

	cloudApiKeysRemoveCmd.Flags().Bool("dry-run", false,
		"Disables the actual removal and simply does a dry-run.")
}
