package cmd

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const (
	capellaPoolKeyUidLen      = 8
	capellaPoolKeyUidAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// capellaPoolKeyPrefix names the keys of one machine's pool. cbdinocluster only
// ever creates, rotates or deletes a Capella API key whose name starts with it.
func capellaPoolKeyPrefix(poolName string) string {
	return fmt.Sprintf("cbdino_pool_%s_", poolName)
}

func newCapellaPoolKeyName(poolName string) (string, error) {
	uid, err := newCapellaPoolKeyUid()
	if err != nil {
		return "", err
	}
	return capellaPoolKeyPrefix(poolName) + uid, nil
}

func newCapellaPoolKeyUid() (string, error) {
	// 36 * 7, so a modulo cannot favour the first letters of the alphabet.
	const maxUnbiased = byte(252)

	uid := make([]byte, 0, capellaPoolKeyUidLen)
	buf := make([]byte, capellaPoolKeyUidLen)
	for len(uid) < capellaPoolKeyUidLen {
		if _, err := rand.Read(buf); err != nil {
			return "", errors.Wrap(err, "failed to read random bytes")
		}

		for _, b := range buf {
			if b >= maxUnbiased {
				continue
			}
			uid = append(uid, capellaPoolKeyUidAlphabet[int(b)%len(capellaPoolKeyUidAlphabet)])
			if len(uid) == capellaPoolKeyUidLen {
				break
			}
		}
	}

	return string(uid), nil
}

func capellaPoolKeyDescription(poolName string) string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("Created by cbdinocluster for %s on %s, %s.",
		poolName, hostname, time.Now().Format("2006-01-02"))
}

type capellaPoolPartition struct {
	// Saved keys Capella still lists.
	Matched []cbdcconfig.Config_CapellaApiKey
	// Saved keys Capella no longer lists, so their secret is worthless.
	Missing []cbdcconfig.Config_CapellaApiKey
	// Pool keys Capella lists that this machine holds no secret for. The v4
	// API never gives a secret back, so only a rotation recovers them.
	Orphans []*capellav4.ApiKeyInfo
}

// partitionCapellaPoolKeys compares the saved pool keys against the pool keys
// Capella reports. The primary key is left out of every group, it is the key
// that manages the pool and must never be rotated or deleted.
func partitionCapellaPoolKeys(
	prefix, primaryKeyID string,
	configKeys []cbdcconfig.Config_CapellaApiKey,
	remoteKeys []*capellav4.ApiKeyInfo,
) capellaPoolPartition {
	remoteByID := make(map[string]*capellav4.ApiKeyInfo, len(remoteKeys))
	for _, remoteKey := range remoteKeys {
		if !strings.HasPrefix(remoteKey.Name, prefix) {
			continue
		}
		remoteByID[remoteKey.ID] = remoteKey
	}

	var out capellaPoolPartition
	configIDs := make(map[string]bool, len(configKeys))
	for _, configKey := range configKeys {
		if configKey.Key != "" {
			configIDs[configKey.Key] = true
		}
		if configKey.Key == primaryKeyID || !strings.HasPrefix(configKey.Name, prefix) {
			continue
		}

		if remoteByID[configKey.Key] != nil {
			out.Matched = append(out.Matched, configKey)
		} else {
			out.Missing = append(out.Missing, configKey)
		}
	}

	for _, remoteKey := range remoteKeys {
		if !strings.HasPrefix(remoteKey.Name, prefix) {
			continue
		}
		if remoteKey.ID == primaryKeyID || configIDs[remoteKey.ID] {
			continue
		}
		out.Orphans = append(out.Orphans, remoteKey)
	}

	return out
}

func countCapellaPoolKeys(
	prefix, primaryKeyID string,
	configKeys []cbdcconfig.Config_CapellaApiKey,
) int {
	count := 0
	for _, configKey := range configKeys {
		if configKey.Key == primaryKeyID || !strings.HasPrefix(configKey.Name, prefix) {
			continue
		}
		count++
	}
	return count
}

// selectCapellaPoolConfigKeys picks the saved keys to act on. Without ids it
// takes the whole pool, otherwise only the ids given, and it refuses an id that
// is not a pool key this machine holds.
func selectCapellaPoolConfigKeys(
	prefix, primaryKeyID string,
	configKeys []cbdcconfig.Config_CapellaApiKey,
	keyIDs []string,
) ([]cbdcconfig.Config_CapellaApiKey, error) {
	poolKeys := make(map[string]cbdcconfig.Config_CapellaApiKey, len(configKeys))
	var allKeys []cbdcconfig.Config_CapellaApiKey
	for _, configKey := range configKeys {
		if configKey.Key == primaryKeyID || !strings.HasPrefix(configKey.Name, prefix) {
			continue
		}
		poolKeys[configKey.Key] = configKey
		allKeys = append(allKeys, configKey)
	}

	if len(keyIDs) == 0 {
		return allKeys, nil
	}

	var selected []cbdcconfig.Config_CapellaApiKey
	for _, keyID := range keyIDs {
		poolKey, ok := poolKeys[keyID]
		if !ok {
			return nil, errors.Errorf("%s is not a saved %s pool key", keyID, prefix)
		}
		selected = append(selected, poolKey)
	}

	return selected, nil
}

// selectCapellaPoolRemoteKeys picks the keys to act on from what Capella
// reports, with the same rules as selectCapellaPoolConfigKeys.
func selectCapellaPoolRemoteKeys(
	prefix, primaryKeyID string,
	remoteKeys []*capellav4.ApiKeyInfo,
	keyIDs []string,
) ([]*capellav4.ApiKeyInfo, error) {
	poolKeys := make(map[string]*capellav4.ApiKeyInfo, len(remoteKeys))
	var allKeys []*capellav4.ApiKeyInfo
	for _, remoteKey := range remoteKeys {
		if remoteKey.ID == primaryKeyID || !strings.HasPrefix(remoteKey.Name, prefix) {
			continue
		}
		poolKeys[remoteKey.ID] = remoteKey
		allKeys = append(allKeys, remoteKey)
	}

	if len(keyIDs) == 0 {
		return allKeys, nil
	}

	var selected []*capellav4.ApiKeyInfo
	for _, keyID := range keyIDs {
		poolKey, ok := poolKeys[keyID]
		if !ok {
			return nil, errors.Errorf("%s is not a %s pool key in capella", keyID, prefix)
		}
		selected = append(selected, poolKey)
	}

	return selected, nil
}

// checkCapellaPoolKeyTarget guards every rotate and delete. A key cbdinocluster
// did not create can belong to anyone in the organization, and the primary key
// is the one that manages the pool.
func checkCapellaPoolKeyTarget(prefix, primaryKeyID, keyID, keyName string) error {
	if keyID == "" {
		return errors.New("refusing to touch a capella api key with no id")
	}
	if keyID == primaryKeyID {
		return errors.Errorf("refusing to touch the primary capella api key %s", keyID)
	}
	if !strings.HasPrefix(keyName, prefix) {
		return errors.Errorf("refusing to touch the capella api key %s, "+
			"its name is not part of the %s pool", keyID, prefix)
	}
	return nil
}

// capellaRequestSecrets picks the secrets the round robin client rotates over.
// Pool keys with a secret carry all the request traffic, which keeps the
// primary key's own rate budget free for pool management and for other tools
// that share it. Without a usable pool key the primary serves alone.
func capellaRequestSecrets(apiKeys []cbdcconfig.Config_CapellaApiKey) []string {
	if len(apiKeys) == 0 {
		return nil
	}

	var poolSecrets []string
	for _, apiKey := range apiKeys[1:] {
		if apiKey.Secret != "" {
			poolSecrets = append(poolSecrets, apiKey.Secret)
		}
	}
	if len(poolSecrets) > 0 {
		return poolSecrets
	}

	if apiKeys[0].Secret == "" {
		return nil
	}
	return []string{apiKeys[0].Secret}
}

func capellaV4EndpointFor(config *cbdcconfig.Config) string {
	endpoint := config.Capella.V4Endpoint
	if endpoint == "" {
		endpoint = os.Getenv("CAPELLA_V4_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = cbdcconfig.DEFAULT_CAPELLA_V4_ENDPOINT
	}
	return endpoint
}

// newCapellaPoolClient builds a client that uses the primary key alone. Key
// management must not run over the round robin pool, a rotation there could
// invalidate a secret another request is using at that moment.
func newCapellaPoolClient(config *cbdcconfig.Config, logger *zap.Logger) (*capellav4.Client, error) {
	if !config.Capella.Enabled.Value() {
		return nil, errors.New("capella is disabled, run `cbdinocluster init` to enable it")
	}
	if len(config.Capella.ApiKeys) == 0 || config.Capella.ApiKeys[0].Secret == "" {
		return nil, errors.New("no primary capella api key is configured, run `cbdinocluster init`")
	}

	client, err := capellav4.NewClient(&capellav4.ClientOptions{
		Logger:     logger,
		Endpoint:   capellaV4EndpointFor(config),
		SecretKeys: []string{config.Capella.ApiKeys[0].Secret},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create capella v4 client")
	}

	return client, nil
}

type capellaPoolSession struct {
	Client       *capellav4.Client
	OrgID        string
	Prefix       string
	PrimaryKeyID string
	PoolName     string
}

func newCapellaPoolSession(config *cbdcconfig.Config, logger *zap.Logger) (*capellaPoolSession, error) {
	client, err := newCapellaPoolClient(config, logger)
	if err != nil {
		return nil, err
	}

	poolName := config.Capella.PoolKeyName
	if poolName == "" {
		return nil, errors.New("no capella api key pool is configured, " +
			"run `cbdinocluster init` and give the pool a name")
	}
	if config.Capella.OrganizationID == "" {
		return nil, errors.New("no capella organization id is configured, run `cbdinocluster init`")
	}

	return &capellaPoolSession{
		Client:       client,
		OrgID:        config.Capella.OrganizationID,
		Prefix:       capellaPoolKeyPrefix(poolName),
		PrimaryKeyID: config.Capella.ApiKeys[0].Key,
		PoolName:     poolName,
	}, nil
}
