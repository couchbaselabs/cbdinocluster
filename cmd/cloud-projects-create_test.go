package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureOutput runs fn and returns what it wrote to stdout and stderr.
func captureOutput(t *testing.T, fn func()) (string, string) {
	t.Helper()

	readAll := func(r *os.File) <-chan string {
		out := make(chan string, 1)
		go func() {
			data, _ := io.ReadAll(r)
			out <- string(data)
		}()
		return out
	}

	stdoutR, stdoutW, err := os.Pipe()
	require.NoError(t, err)
	stderrR, stderrW, err := os.Pipe()
	require.NoError(t, err)

	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	stdoutC, stderrC := readAll(stdoutR), readAll(stderrR)

	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()
	fn()

	require.NoError(t, stdoutW.Close())
	require.NoError(t, stderrW.Close())
	return <-stdoutC, <-stderrC
}

// TestCloudProjectsCreateOutput runs the command against a stub v4 api with no
// project ID in the config.
func TestCloudProjectsCreateOutput(t *testing.T) {
	t.Setenv("CAPELLA_PROJECT_ID", "")

	var mu sync.Mutex
	var creates []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v4/organizations/org-1/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"p-other","name":"someone-else"}]}`))
	})
	mux.HandleFunc("POST /v4/organizations/org-1/projects", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		creates = append(creates, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"p-created"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cbdcconfig.SetConfigPathOverride(configPath)
	t.Cleanup(func() {
		cbdcconfig.SetConfigPathOverride("")
		rootCmd.SetArgs(nil)
		_ = rootCmd.PersistentFlags().Set("config", "")
		_ = rootCmd.PersistentFlags().Set("json", "false")
		_ = cloudProjectsCreateCmd.Flags().Set("description", "")
	})

	config := &cbdcconfig.Config{Version: cbdcconfig.Version}
	config.Capella.Enabled.Set(true)
	config.Capella.V4Endpoint = srv.URL
	config.Capella.OrganizationID = "org-1"
	config.Capella.ApiKeys = []cbdcconfig.Config_CapellaApiKey{{Secret: "test-secret"}}
	require.NoError(t, cbdcconfig.Save(t.Context(), config))

	run := func(extraArgs ...string) (string, string) {
		args := append([]string{"cloud", "projects", "create", "team-project",
			"--description", "Clusters of the team", "--config", configPath}, extraArgs...)
		rootCmd.SetArgs(args)
		return captureOutput(t, func() {
			require.NoError(t, rootCmd.Execute())
		})
	}

	takeCreates := func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := creates
		creates = nil
		return out
	}

	t.Run("plain", func(t *testing.T) {
		stdout, stderr := run()
		creates := takeCreates()
		assert.Equal(t, "p-created\n", stdout)
		assert.Contains(t, stderr, "cbdinocluster init --capella-project-id p-created")
		require.Len(t, creates, 1)
		assert.Equal(t, "team-project", creates[0]["name"])
		assert.Equal(t, "Clusters of the team", creates[0]["description"])
	})

	t.Run("json", func(t *testing.T) {
		stdout, _ := run("--json")
		assert.JSONEq(t, `{"id":"p-created","name":"team-project"}`, stdout)
		assert.Len(t, takeCreates(), 1)
	})
}
