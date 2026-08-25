package cmd

import (
	"strings"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapellaPoolKeyPrefix(t *testing.T) {
	assert.Equal(t, "cbdino_pool_emilienbev_", capellaPoolKeyPrefix("emilienbev"))
}

// A duplicate name is rejected by Capella, so the uid must be unique and it
// must stay inside the alphabet the key name allows.
func TestNewCapellaPoolKeyName(t *testing.T) {
	const draws = 5000

	prefix := capellaPoolKeyPrefix("test")
	seen := make(map[string]bool, draws)

	for i := 0; i < draws; i++ {
		name, err := newCapellaPoolKeyName("test")
		require.NoError(t, err)

		require.True(t, strings.HasPrefix(name, prefix), "name %s has no pool prefix", name)

		uid := strings.TrimPrefix(name, prefix)
		require.Len(t, uid, capellaPoolKeyUidLen)
		for _, char := range uid {
			require.True(t, strings.ContainsRune(capellaPoolKeyUidAlphabet, char),
				"uid %s uses the unexpected character %q", uid, char)
		}

		require.False(t, seen[uid], "uid %s was drawn twice", uid)
		seen[uid] = true
	}
}

func TestPartitionCapellaPoolKeys(t *testing.T) {
	const prefix = "cbdino_pool_test_"
	const primaryKeyID = "primary"

	configKeys := []cbdcconfig.Config_CapellaApiKey{
		{Key: primaryKeyID, Secret: "primary-secret"},
		{Key: "hand-entered", Secret: "hand-secret"},
		{Key: "pool-kept", Secret: "kept-secret", Name: prefix + "aaaaaaaa"},
		{Key: "pool-gone", Secret: "gone-secret", Name: prefix + "bbbbbbbb"},
		{Key: "other-pool", Secret: "other-secret", Name: "cbdino_pool_other_cccccccc"},
	}
	remoteKeys := []*capellav4.ApiKeyInfo{
		{ID: primaryKeyID, Name: "hand made primary"},
		{ID: "pool-kept", Name: prefix + "aaaaaaaa"},
		{ID: "pool-orphan", Name: prefix + "dddddddd"},
		{ID: "someone-else", Name: "someone else's key"},
		{ID: "other-pool-remote", Name: "cbdino_pool_other_eeeeeeee"},
	}

	parts := partitionCapellaPoolKeys(prefix, primaryKeyID, configKeys, remoteKeys)

	require.Len(t, parts.Matched, 1)
	assert.Equal(t, "pool-kept", parts.Matched[0].Key)

	require.Len(t, parts.Missing, 1)
	assert.Equal(t, "pool-gone", parts.Missing[0].Key)

	require.Len(t, parts.Orphans, 1)
	assert.Equal(t, "pool-orphan", parts.Orphans[0].ID)
}

// The primary key manages the pool, so it must stay out of every group even
// when its name happens to match the pool prefix.
func TestPartitionCapellaPoolKeysSkipsThePrimaryKey(t *testing.T) {
	const prefix = "cbdino_pool_test_"
	const primaryKeyID = "primary"

	configKeys := []cbdcconfig.Config_CapellaApiKey{
		{Key: primaryKeyID, Secret: "primary-secret", Name: prefix + "aaaaaaaa"},
	}
	remoteKeys := []*capellav4.ApiKeyInfo{
		{ID: primaryKeyID, Name: prefix + "aaaaaaaa"},
	}

	parts := partitionCapellaPoolKeys(prefix, primaryKeyID, configKeys, remoteKeys)

	assert.Empty(t, parts.Matched)
	assert.Empty(t, parts.Missing)
	assert.Empty(t, parts.Orphans)
}

func TestCountCapellaPoolKeys(t *testing.T) {
	const prefix = "cbdino_pool_test_"

	configKeys := []cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "pool-one", Secret: "one", Name: prefix + "aaaaaaaa"},
		{Key: "pool-two", Secret: "two", Name: prefix + "bbbbbbbb"},
		{Key: "other-pool", Secret: "three", Name: "cbdino_pool_other_cccccccc"},
	}

	assert.Equal(t, 2, countCapellaPoolKeys(prefix, "primary", configKeys))
}

func TestCheckCapellaPoolKeyTarget(t *testing.T) {
	const prefix = "cbdino_pool_test_"

	require.NoError(t, checkCapellaPoolKeyTarget(prefix, "primary", "pool-one", prefix+"aaaaaaaa"))

	require.Error(t, checkCapellaPoolKeyTarget(prefix, "primary", "", prefix+"aaaaaaaa"))
	require.Error(t, checkCapellaPoolKeyTarget(prefix, "primary", "primary", prefix+"aaaaaaaa"))
	require.Error(t, checkCapellaPoolKeyTarget(prefix, "primary", "pool-one", "someone else's key"))
	require.Error(t, checkCapellaPoolKeyTarget(prefix, "primary", "pool-one", "cbdino_pool_other_aaaaaaaa"))
}

func TestSelectCapellaPoolConfigKeys(t *testing.T) {
	const prefix = "cbdino_pool_test_"

	configKeys := []cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "hand-entered", Secret: "hand-secret"},
		{Key: "pool-one", Secret: "one", Name: prefix + "aaaaaaaa"},
		{Key: "pool-two", Secret: "two", Name: prefix + "bbbbbbbb"},
	}

	all, err := selectCapellaPoolConfigKeys(prefix, "primary", configKeys, nil)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "pool-one", all[0].Key)
	assert.Equal(t, "pool-two", all[1].Key)

	some, err := selectCapellaPoolConfigKeys(prefix, "primary", configKeys, []string{"pool-two"})
	require.NoError(t, err)
	require.Len(t, some, 1)
	assert.Equal(t, "pool-two", some[0].Key)

	_, err = selectCapellaPoolConfigKeys(prefix, "primary", configKeys, []string{"primary"})
	require.Error(t, err)

	_, err = selectCapellaPoolConfigKeys(prefix, "primary", configKeys, []string{"hand-entered"})
	require.Error(t, err)

	_, err = selectCapellaPoolConfigKeys(prefix, "primary", configKeys, []string{"unknown"})
	require.Error(t, err)
}

func TestSelectCapellaPoolRemoteKeys(t *testing.T) {
	const prefix = "cbdino_pool_test_"

	remoteKeys := []*capellav4.ApiKeyInfo{
		{ID: "primary", Name: "hand made primary"},
		{ID: "pool-one", Name: prefix + "aaaaaaaa"},
		{ID: "someone-else", Name: "someone else's key"},
	}

	all, err := selectCapellaPoolRemoteKeys(prefix, "primary", remoteKeys, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "pool-one", all[0].ID)

	_, err = selectCapellaPoolRemoteKeys(prefix, "primary", remoteKeys, []string{"someone-else"})
	require.Error(t, err)

	_, err = selectCapellaPoolRemoteKeys(prefix, "primary", remoteKeys, []string{"primary"})
	require.Error(t, err)
}

// The primary key stays out of the request rotation as soon as one pool key
// holds a secret, and serves alone otherwise.
func TestCapellaRequestSecrets(t *testing.T) {
	const prefix = "cbdino_pool_test_"

	primary := cbdcconfig.Config_CapellaApiKey{Key: "primary", Secret: "primary-secret"}
	poolOne := cbdcconfig.Config_CapellaApiKey{Key: "pool-one", Secret: "one", Name: prefix + "aaaaaaaa"}
	poolTwo := cbdcconfig.Config_CapellaApiKey{Key: "pool-two", Secret: "two", Name: prefix + "bbbbbbbb"}
	poolNoSecret := cbdcconfig.Config_CapellaApiKey{Key: "pool-bare", Name: prefix + "cccccccc"}

	assert.Nil(t, capellaRequestSecrets(nil))
	assert.Nil(t, capellaRequestSecrets([]cbdcconfig.Config_CapellaApiKey{{Key: "primary"}}))

	assert.Equal(t, []string{"primary-secret"},
		capellaRequestSecrets([]cbdcconfig.Config_CapellaApiKey{primary}))
	assert.Equal(t, []string{"primary-secret"},
		capellaRequestSecrets([]cbdcconfig.Config_CapellaApiKey{primary, poolNoSecret}))

	assert.Equal(t, []string{"one", "two"},
		capellaRequestSecrets([]cbdcconfig.Config_CapellaApiKey{primary, poolOne, poolTwo}))
}

func TestNewCapellaPoolSessionRequiresConfiguration(t *testing.T) {
	enabled := cbdcconfig.StringBool("true")

	_, err := newCapellaPoolSession(&cbdcconfig.Config{}, nil)
	require.Error(t, err)

	_, err = newCapellaPoolSession(&cbdcconfig.Config{
		Capella: cbdcconfig.Config_Capella{Enabled: enabled},
	}, nil)
	require.Error(t, err)

	_, err = newCapellaPoolSession(&cbdcconfig.Config{
		Capella: cbdcconfig.Config_Capella{
			Enabled:        enabled,
			ApiKeys:        []cbdcconfig.Config_CapellaApiKey{{Key: "primary", Secret: "secret"}},
			OrganizationID: "org",
		},
	}, nil)
	require.Error(t, err, "a session must not start without a pool name")

	session, err := newCapellaPoolSession(&cbdcconfig.Config{
		Capella: cbdcconfig.Config_Capella{
			Enabled:        enabled,
			ApiKeys:        []cbdcconfig.Config_CapellaApiKey{{Key: "primary", Secret: "secret"}},
			OrganizationID: "org",
			PoolKeyName:    "test",
			V4Endpoint:     "http://127.0.0.1:1",
		},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "cbdino_pool_test_", session.Prefix)
	assert.Equal(t, "primary", session.PrimaryKeyID)
	assert.Equal(t, "org", session.OrgID)
}
