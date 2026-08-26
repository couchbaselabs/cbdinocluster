package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestAppendApiKeys covers the dedup that every branch of init relies on, so a
// rerun cannot shrink the pool by re-adding a secret it already holds.
func TestAppendApiKeys(t *testing.T) {
	tests := []struct {
		name  string
		pool  []cbdcconfig.Config_CapellaApiKey
		extra []cbdcconfig.Config_CapellaApiKey
		want  []cbdcconfig.Config_CapellaApiKey
	}{
		{
			name: "empty secret skipped",
			extra: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one", Secret: ""},
				{Key: "id-two", Secret: "secret-two"},
			},
			want: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-two", Secret: "secret-two"},
			},
		},
		{
			name: "secret already in the pool skipped",
			pool: []cbdcconfig.Config_CapellaApiKey{{Key: "primary", Secret: "secret-one"}},
			extra: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one", Secret: "secret-one"},
				{Key: "id-two", Secret: "secret-two"},
			},
			want: []cbdcconfig.Config_CapellaApiKey{
				{Key: "primary", Secret: "secret-one"},
				{Key: "id-two", Secret: "secret-two"},
			},
		},
		{
			name: "duplicate inside extra skipped",
			extra: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one", Secret: "secret-one"},
				{Key: "id-two", Secret: "secret-one"},
			},
			want: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one", Secret: "secret-one"},
			},
		},
		{
			name: "same secret under two ids kept once",
			pool: []cbdcconfig.Config_CapellaApiKey{{Key: "id-one", Secret: "secret-one"}},
			extra: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one-renamed", Secret: "secret-one"},
			},
			want: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one", Secret: "secret-one"},
			},
		},
		{
			name: "nothing to add",
			pool: []cbdcconfig.Config_CapellaApiKey{{Key: "id-one", Secret: "secret-one"}},
			want: []cbdcconfig.Config_CapellaApiKey{
				{Key: "id-one", Secret: "secret-one"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendApiKeys(tt.pool, tt.extra...)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestKeepApiKeyQuestion(t *testing.T) {
	require.Equal(t, "Keep the saved Capella API key #2 (id-two)?",
		keepApiKeyQuestion(1, cbdcconfig.Config_CapellaApiKey{Key: "id-two", Secret: "secret-two"}))
	require.Equal(t, "Keep the saved Capella API key #2?",
		keepApiKeyQuestion(1, cbdcconfig.Config_CapellaApiKey{Secret: "secret-two"}))
	require.Equal(t, "Keep the saved Capella API key #2 (id-two, cbdino_pool_test_aaaaaaaa)?",
		keepApiKeyQuestion(1, cbdcconfig.Config_CapellaApiKey{
			Key: "id-two", Secret: "secret-two", Name: "cbdino_pool_test_aaaaaaaa"}))
	require.Equal(t, "Keep the saved Capella API key #2 (cbdino_pool_test_aaaaaaaa)?",
		keepApiKeyQuestion(1, cbdcconfig.Config_CapellaApiKey{
			Secret: "secret-two", Name: "cbdino_pool_test_aaaaaaaa"}))
}

// fakeCapellaApiKeys serves the v4 API key endpoints the init pool section
// uses, so no test ever reaches the real Capella API.
type fakeCapellaApiKeys struct {
	mu sync.Mutex

	keys         []*capellav4.ApiKeyInfo
	failCreateAt int

	// rateLimitFirstCreates answers 429 to that many create requests, then lets
	// the pool grow. rateLimitPastCreates answers 429 to every create request
	// once that many creates succeeded. retryAfter is sent with each 429.
	rateLimitFirstCreates int
	rateLimitPastCreates  int
	retryAfter            string

	requests       int
	creates        int
	created        int
	createExpiries []float64
	createAuths    []string
	rotated        []string
	deleted        []string
	deleteAuths    map[string]string
}

func (f *fakeCapellaApiKeys) start(t *testing.T) string {
	t.Helper()

	const basePath = "/v4/organizations/org/apikeys"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++

		if !strings.HasPrefix(r.URL.Path, basePath) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		subPath := strings.TrimPrefix(r.URL.Path, basePath)

		switch {
		case r.Method == http.MethodGet && subPath == "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": f.keys,
				"cursor": map[string]any{"pages": map[string]any{
					"page": 1, "last": 1, "perPage": 100, "totalItems": len(f.keys)}},
			})

		case r.Method == http.MethodPost && subPath == "":
			f.creates++
			f.createAuths = append(f.createAuths, r.Header.Get("Authorization"))

			if f.creates <= f.rateLimitFirstCreates ||
				(f.rateLimitPastCreates > 0 && f.created >= f.rateLimitPastCreates) {
				if f.retryAfter != "" {
					w.Header().Set("Retry-After", f.retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
				return
			}

			if f.creates == f.failCreateAt {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"code":4000,"httpStatusCode":422,"message":"key limit reached"}`))
				return
			}

			var req capellav4.CreateApiKeyRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			f.createExpiries = append(f.createExpiries, req.Expiry)

			f.created++
			keyID := fmt.Sprintf("created-%d", f.created)
			f.keys = append(f.keys, &capellav4.ApiKeyInfo{ID: keyID, Name: req.Name})

			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": keyID, "token": "secret-" + keyID})

		case r.Method == http.MethodPost && strings.HasSuffix(subPath, "/rotate"):
			keyID := strings.TrimSuffix(strings.TrimPrefix(subPath, "/"), "/rotate")
			f.rotated = append(f.rotated, keyID)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"secretKey": keyID, "token": "rotated-" + keyID})

		case r.Method == http.MethodDelete && subPath != "":
			keyID := strings.TrimPrefix(subPath, "/")
			f.deleted = append(f.deleted, keyID)
			if f.deleteAuths == nil {
				f.deleteAuths = make(map[string]string)
			}
			f.deleteAuths[keyID] = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func (f *fakeCapellaApiKeys) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeCapellaApiKeys) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}

func (f *fakeCapellaApiKeys) createdExpiries() []float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]float64(nil), f.createExpiries...)
}

func (f *fakeCapellaApiKeys) rotatedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.rotated...)
}

func (f *fakeCapellaApiKeys) deletedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func (f *fakeCapellaApiKeys) createAuthorizations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.createAuths...)
}

func (f *fakeCapellaApiKeys) deleteAuthorizations() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.deleteAuths)
}

func writeTestConfig(t *testing.T, config *cbdcconfig.Config) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "cbdinocluster.yaml")
	configBytes, err := yaml.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, configBytes, 0600))

	return configPath
}

func readTestConfig(t *testing.T, configPath string) *cbdcconfig.Config {
	t.Helper()

	configBytes, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var config *cbdcconfig.Config
	require.NoError(t, yaml.Unmarshal(configBytes, &config))

	return config
}

// runTestInit drives the real init command with every non Capella provider
// disabled. The config path override and CBDINOCLUSTER_CONFIG both point at a
// throwaway file, so the developer's own config is never touched.
func runTestInit(t *testing.T, configPath, v4Endpoint string, poolArgs ...string) {
	t.Helper()

	t.Setenv(cbdcconfig.EnvConfigPath, configPath)
	for _, envName := range []string{
		"CAPELLA_V4_ENDPOINT", "CAPELLA_API_KEY", "CAPELLA_API_SECRET",
		"CAPELLA_ENDPOINT", "CAPELLA_USER", "CAPELLA_PASS",
		"CAPELLA_OID", "CAPELLA_OVERRIDE_TOKEN", "CAPELLA_INTERNAL_SUPPORT_TOKEN",
	} {
		t.Setenv(envName, "")
	}

	t.Cleanup(func() {
		cbdcconfig.SetConfigPathOverride("")
		rootCmd.SetArgs(nil)
	})

	// Cobra flags are process globals, so a value set by one Execute leaks into
	// the next. The expiry flag is the only pool flag the base args leave unset.
	expiryFlag := initCmd.Flags().Lookup("capella-pool-expiry")
	require.NotNil(t, expiryFlag)
	require.NoError(t, expiryFlag.Value.Set(expiryFlag.DefValue))
	expiryFlag.Changed = false

	args := []string{"init", "--auto",
		"--config", configPath,
		"--disable-github", "--disable-docker", "--disable-k8s",
		"--disable-aws", "--disable-azure", "--disable-gcp", "--disable-dns",
		"--capella-v4-endpoint", v4Endpoint,
		"--capella-create-pool=false",
		"--capella-pool-name=",
		"--capella-pool-size=10",
	}
	args = append(args, poolArgs...)

	rootCmd.SetArgs(args)
	require.NoError(t, rootCmd.Execute())
}

func testCapellaConfig(apiKeys []cbdcconfig.Config_CapellaApiKey, poolName string) *cbdcconfig.Config {
	config := &cbdcconfig.Config{Version: cbdcconfig.Version}
	config.Capella.Enabled.Set(true)
	config.Capella.ApiKeys = apiKeys
	config.Capella.PoolKeyName = poolName
	config.Capella.OrganizationID = "org"
	config.Capella.Endpoint = cbdcconfig.DEFAULT_CAPELLA_ENDPOINT
	config.Capella.DefaultCloud = cbdcconfig.DEFAULT_CAPELLA_PROVIDER
	config.Capella.DefaultAwsRegion = cbdcconfig.DEFAULT_AWS_REGION
	config.Capella.DefaultAzureRegion = cbdcconfig.DEFAULT_AZURE_REGION
	config.Capella.DefaultGcpRegion = cbdcconfig.DEFAULT_GCP_REGION
	return config
}

// fit-cli runs `init --auto` on every job, so it must reproduce the saved keys
// without spending a single Capella API call.
func TestInitAutoLeavesTheSavedPoolAlone(t *testing.T) {
	fake := &fakeCapellaApiKeys{}
	endpoint := fake.start(t)

	savedApiKeys := []cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "pool-one", Secret: "pool-one-secret", Name: "cbdino_pool_test_aaaaaaaa"},
		{Key: "pool-two", Secret: "pool-two-secret", Name: "cbdino_pool_test_bbbbbbbb"},
	}
	configPath := writeTestConfig(t, testCapellaConfig(savedApiKeys, "test"))

	runTestInit(t, configPath, endpoint)

	assert.Equal(t, 0, fake.requestCount(), "init --auto must not call the Capella API")

	savedConfig := readTestConfig(t, configPath)
	assert.Equal(t, savedApiKeys, savedConfig.Capella.ApiKeys)
	assert.Equal(t, "test", savedConfig.Capella.PoolKeyName)
}

// Capella returns a secret once only, so every created key must reach the disk
// before the next one is created.
func TestInitCreatesPoolKeysAndSavesEachSecret(t *testing.T) {
	fake := &fakeCapellaApiKeys{failCreateAt: 3}
	endpoint := fake.start(t)

	configPath := writeTestConfig(t, testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
	}, ""))

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-name=test",
		"--capella-pool-size=3",
		"--capella-pool-expiry=1")

	savedConfig := readTestConfig(t, configPath)
	assert.Equal(t, "test", savedConfig.Capella.PoolKeyName)
	assert.Equal(t, 3, fake.createCount(), "init must ask for the three missing keys")
	// The failing third create is refused before its body is read.
	assert.Equal(t, []float64{1, 1}, fake.createdExpiries())

	// The third create failed, so only the first two secrets exist at all.
	require.Len(t, savedConfig.Capella.ApiKeys, 3)
	assert.Equal(t, "primary", savedConfig.Capella.ApiKeys[0].Key)
	assert.Equal(t, "created-1", savedConfig.Capella.ApiKeys[1].Key)
	assert.Equal(t, "secret-created-1", savedConfig.Capella.ApiKeys[1].Secret)
	assert.Equal(t, "created-2", savedConfig.Capella.ApiKeys[2].Key)
	assert.Equal(t, "secret-created-2", savedConfig.Capella.ApiKeys[2].Secret)

	for _, createdKey := range savedConfig.Capella.ApiKeys[1:] {
		assert.True(t, strings.HasPrefix(createdKey.Name, "cbdino_pool_test_"),
			"created key %s has no pool name", createdKey.Key)
	}
}

// A machine with no pool name yet names its pool after the GitHub user, so the
// keys it creates carry an owner anyone can read in the Capella UI.
func TestInitNamesThePoolAfterTheGitHubUser(t *testing.T) {
	fake := &fakeCapellaApiKeys{}
	endpoint := fake.start(t)

	config := testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
	}, "")
	config.GitHub.User = "ghuser"
	configPath := writeTestConfig(t, config)

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-size=1")

	savedConfig := readTestConfig(t, configPath)
	assert.Equal(t, "ghuser", savedConfig.Capella.PoolKeyName)
	assert.Equal(t, []float64{0.5}, fake.createdExpiries(),
		"a key created without the expiry flag must default to half a day")

	require.Len(t, savedConfig.Capella.ApiKeys, 2)
	assert.True(t, strings.HasPrefix(savedConfig.Capella.ApiKeys[1].Name, "cbdino_pool_ghuser_"),
		"created key %s has no pool name", savedConfig.Capella.ApiKeys[1].Key)
}

// A key deleted in the Capella UI leaves a secret that cannot work any more.
func TestInitDropsPoolKeysCapellaNoLongerLists(t *testing.T) {
	fake := &fakeCapellaApiKeys{keys: []*capellav4.ApiKeyInfo{
		{ID: "primary", Name: "a hand made key"},
		{ID: "pool-one", Name: "cbdino_pool_test_aaaaaaaa"},
	}}
	endpoint := fake.start(t)

	configPath := writeTestConfig(t, testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "pool-one", Secret: "pool-one-secret", Name: "cbdino_pool_test_aaaaaaaa"},
		{Key: "pool-gone", Secret: "pool-gone-secret", Name: "cbdino_pool_test_bbbbbbbb"},
	}, "test"))

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-size=1")

	savedConfig := readTestConfig(t, configPath)
	assert.Equal(t, []cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "pool-one", Secret: "pool-one-secret", Name: "cbdino_pool_test_aaaaaaaa"},
	}, savedConfig.Capella.ApiKeys)

	assert.Empty(t, fake.rotatedKeys())
	assert.Empty(t, fake.deletedKeys())
	assert.Equal(t, 0, fake.createCount(), "the pool already holds the requested number of keys")
}

// The v4 API never gives a secret back, so a pool key this machine lost the
// secret of is only usable again after a rotation.
func TestInitRotatesPoolKeysWithNoSavedSecret(t *testing.T) {
	fake := &fakeCapellaApiKeys{keys: []*capellav4.ApiKeyInfo{
		{ID: "primary", Name: "a hand made key"},
		{ID: "pool-orphan", Name: "cbdino_pool_test_aaaaaaaa"},
	}}
	endpoint := fake.start(t)

	configPath := writeTestConfig(t, testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
	}, "test"))

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-size=1")

	assert.Equal(t, []string{"pool-orphan"}, fake.rotatedKeys())
	assert.Empty(t, fake.deletedKeys())
	assert.Equal(t, 0, fake.createCount())

	savedConfig := readTestConfig(t, configPath)
	assert.Equal(t, []cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "pool-orphan", Secret: "rotated-pool-orphan", Name: "cbdino_pool_test_aaaaaaaa"},
	}, savedConfig.Capella.ApiKeys)
}

// sleepRecorder stands in for the rate limit wait, so a test can check the
// delay without spending it.
type sleepRecorder struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleepRecorder) sleep(wait time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, wait)
}

func (s *sleepRecorder) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

func recordCapellaPoolKeyWaits(t *testing.T) *sleepRecorder {
	t.Helper()

	recorder := &sleepRecorder{}
	previousSleep := capellaPoolKeySleep
	capellaPoolKeySleep = recorder.sleep
	t.Cleanup(func() { capellaPoolKeySleep = previousSleep })

	return recorder
}

// runTestCloudApiKeysRemove drives the real remove command against a throwaway
// config, with the same isolation runTestInit uses.
func runTestCloudApiKeysRemove(t *testing.T, configPath string, args ...string) {
	t.Helper()

	t.Setenv(cbdcconfig.EnvConfigPath, configPath)
	for _, envName := range []string{
		"CAPELLA_V4_ENDPOINT", "CAPELLA_API_KEY", "CAPELLA_API_SECRET",
		"CAPELLA_ENDPOINT", "CAPELLA_USER", "CAPELLA_PASS",
		"CAPELLA_OID", "CAPELLA_OVERRIDE_TOKEN", "CAPELLA_INTERNAL_SUPPORT_TOKEN",
	} {
		t.Setenv(envName, "")
	}

	t.Cleanup(func() {
		cbdcconfig.SetConfigPathOverride("")
		rootCmd.SetArgs(nil)
	})

	cmdArgs := []string{"cloud", "apikeys", "remove", "--config", configPath}
	cmdArgs = append(cmdArgs, args...)

	rootCmd.SetArgs(cmdArgs)
	require.NoError(t, rootCmd.Execute())
}

// A created key joins the ring at once, so the creates that follow spread over
// the pool instead of spending the primary key's whole rate budget.
func TestInitGrowsTheRingOnCreate(t *testing.T) {
	fake := &fakeCapellaApiKeys{}
	endpoint := fake.start(t)

	configPath := writeTestConfig(t, testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
	}, ""))

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-name=test",
		"--capella-pool-size=3")

	createAuths := fake.createAuthorizations()
	require.Len(t, createAuths, 3)

	createdTokens := map[string]bool{
		"Bearer secret-created-1": true,
		"Bearer secret-created-2": true,
	}
	usedCreated := false
	for _, auth := range createAuths[1:] {
		if createdTokens[auth] {
			usedCreated = true
		}
	}
	assert.True(t, usedCreated,
		"a create after the first must ride a created pool key, got %v", createAuths)
}

// Pool creation is the one call that waits, because a fresh machine holds the
// primary key alone and a whole ring can be busy at the same time.
func TestInitCreateWaitsOutARateLimitedRing(t *testing.T) {
	recorder := recordCapellaPoolKeyWaits(t)

	fake := &fakeCapellaApiKeys{rateLimitFirstCreates: 2, retryAfter: "7"}
	endpoint := fake.start(t)

	configPath := writeTestConfig(t, testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
	}, ""))

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-name=test",
		"--capella-pool-size=1")

	assert.Equal(t, []time.Duration{7 * time.Second, 7 * time.Second}, recorder.recorded())
	assert.Equal(t, 3, fake.createCount())

	savedConfig := readTestConfig(t, configPath)
	require.Len(t, savedConfig.Capella.ApiKeys, 2)
	assert.Equal(t, "created-1", savedConfig.Capella.ApiKeys[1].Key)
	assert.Equal(t, "secret-created-1", savedConfig.Capella.ApiKeys[1].Secret)
}

// A ring that stays rate limited must give up, so init cannot hang a job.
func TestInitCreateFailsAfterThreeWaits(t *testing.T) {
	recorder := recordCapellaPoolKeyWaits(t)

	fake := &fakeCapellaApiKeys{rateLimitPastCreates: 1, retryAfter: "7"}
	endpoint := fake.start(t)

	configPath := writeTestConfig(t, testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
	}, ""))

	runTestInit(t, configPath, endpoint,
		"--capella-create-pool=true",
		"--capella-pool-name=test",
		"--capella-pool-size=2")

	assert.Equal(t, []time.Duration{7 * time.Second, 7 * time.Second, 7 * time.Second},
		recorder.recorded())
	// One create succeeds, then the second one sweeps a two key ring four times.
	assert.Equal(t, 9, fake.createCount())

	savedConfig := readTestConfig(t, configPath)
	require.Len(t, savedConfig.Capella.ApiKeys, 2)
	assert.Equal(t, "primary", savedConfig.Capella.ApiKeys[0].Key)
	assert.Equal(t, "created-1", savedConfig.Capella.ApiKeys[1].Key)
	assert.Equal(t, "secret-created-1", savedConfig.Capella.ApiKeys[1].Secret)
}

// A deleted key cannot authorize anything, so its own secret must leave the
// ring before the delete request goes out.
func TestCloudApiKeysRemoveNeverSelfAuthorizesADelete(t *testing.T) {
	fake := &fakeCapellaApiKeys{keys: []*capellav4.ApiKeyInfo{
		{ID: "primary", Name: "a hand made key"},
		{ID: "pool-one", Name: "cbdino_pool_test_aaaaaaaa"},
		{ID: "pool-two", Name: "cbdino_pool_test_bbbbbbbb"},
	}}
	endpoint := fake.start(t)

	config := testCapellaConfig([]cbdcconfig.Config_CapellaApiKey{
		{Key: "primary", Secret: "primary-secret"},
		{Key: "pool-one", Secret: "pool-one-secret", Name: "cbdino_pool_test_aaaaaaaa"},
		{Key: "pool-two", Secret: "pool-two-secret", Name: "cbdino_pool_test_bbbbbbbb"},
	}, "test")
	config.Capella.V4Endpoint = endpoint
	configPath := writeTestConfig(t, config)

	runTestCloudApiKeysRemove(t, configPath)

	assert.ElementsMatch(t, []string{"pool-one", "pool-two"}, fake.deletedKeys())

	deleteAuths := fake.deleteAuthorizations()
	require.Contains(t, deleteAuths, "pool-one")
	require.Contains(t, deleteAuths, "pool-two")
	assert.NotEqual(t, "Bearer pool-one-secret", deleteAuths["pool-one"])
	assert.NotEqual(t, "Bearer pool-two-secret", deleteAuths["pool-two"])
}
