package cmd

import (
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var cloudApiKeysRotateCmd = &cobra.Command{
	Use:   "rotate [key-id ...]",
	Short: "Rotates the secrets of the pool Capella API keys",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()
		config := helper.GetConfig(ctx)

		session, err := newCapellaPoolSession(config, helper.GetInternalLogger())
		if err != nil {
			logger.Fatal("cannot access the capella api key pool", zap.Error(err))
		}

		targetKeys, err := selectCapellaPoolConfigKeys(session.Prefix, session.PrimaryKeyID,
			config.Capella.ApiKeys, args)
		if err != nil {
			logger.Fatal("failed to select the capella api keys to rotate", zap.Error(err))
		}
		if len(targetKeys) == 0 {
			fmt.Printf("There are no pool Capella API keys to rotate.\n")
			return
		}

		failed := false
		for _, targetKey := range targetKeys {
			err := checkCapellaPoolKeyTarget(session.Prefix, session.PrimaryKeyID,
				targetKey.Key, targetKey.Name)
			if err != nil {
				logger.Fatal("refused to rotate a capella api key", zap.Error(err))
			}

			rotateResp, err := session.Client.RotateApiKey(ctx, session.OrgID, targetKey.Key)
			if err != nil {
				failed = true
				logger.Error("failed to rotate a capella api key",
					zap.String("keyId", targetKey.Key), zap.Error(err))
				continue
			}

			// The old secret stops working at once, so the new one is saved
			// before the next key is rotated.
			for i := range config.Capella.ApiKeys {
				if config.Capella.ApiKeys[i].Key == targetKey.Key {
					config.Capella.ApiKeys[i].Secret = rotateResp.Token
				}
			}

			err = cbdcconfig.Save(ctx, config)
			if err != nil {
				logger.Fatal("failed to save the rotated capella api key secret",
					zap.String("keyId", targetKey.Key), zap.Error(err))
			}

			fmt.Printf("Rotated the Capella API key %s (%s).\n", targetKey.Key, targetKey.Name)
		}

		if failed {
			logger.Fatal("one or more capella api keys could not be rotated")
		}
	},
}

func init() {
	cloudApiKeysCmd.AddCommand(cloudApiKeysRotateCmd)
}
