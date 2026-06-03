package cbdcconfig_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/stretchr/testify/require"
)

// setHomeDir points os.UserHomeDir at dir on every platform. Unix reads $HOME,
// Windows reads %USERPROFILE%, so setting both keeps these tests deterministic
// no matter where `go test` runs.
func setHomeDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestConfigPathDefault(t *testing.T) {
	cbdcconfig.SetConfigPathOverride("")
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })
	t.Setenv(cbdcconfig.EnvConfigPath, "")
	home := t.TempDir()
	setHomeDir(t, home)

	got, err := cbdcconfig.ConfigPath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".cbdinocluster"), got)
}

func TestConfigPathFromEnv(t *testing.T) {
	cbdcconfig.SetConfigPathOverride("")
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })
	setHomeDir(t, t.TempDir())
	envPath := filepath.Join(t.TempDir(), "from-env.yaml")
	t.Setenv(cbdcconfig.EnvConfigPath, envPath)

	got, err := cbdcconfig.ConfigPath()
	require.NoError(t, err)
	require.Equal(t, envPath, got)
}

func TestConfigPathOverrideBeatsEnv(t *testing.T) {
	setHomeDir(t, t.TempDir())
	t.Setenv(cbdcconfig.EnvConfigPath, filepath.Join(t.TempDir(), "from-env.yaml"))
	overridePath := filepath.Join(t.TempDir(), "from-flag.yaml")
	cbdcconfig.SetConfigPathOverride(overridePath)
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })

	got, err := cbdcconfig.ConfigPath()
	require.NoError(t, err)
	require.Equal(t, overridePath, got)
}

// TestLoadSaveRoundTripWithOverride proves the override actually redirects the
// file I/O: a config saved under the override path must be readable back, and
// it must NOT touch the default (~/.cbdinocluster) location — the property the
// shared-CI-home use case depends on.
func TestLoadSaveRoundTripWithOverride(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	setHomeDir(t, home)
	t.Setenv(cbdcconfig.EnvConfigPath, "")

	// Point at a path whose parent directory does not exist yet, mirroring the
	// per-job CI directory use case. Save must create it.
	overridePath := filepath.Join(t.TempDir(), "nested", "dir", "isolated.yaml")
	cbdcconfig.SetConfigPathOverride(overridePath)
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })

	cfg := &cbdcconfig.Config{Version: cbdcconfig.Version}
	cfg.Docker.Enabled.Set(true)
	cfg.Docker.Network = "isolated-net"
	require.NoError(t, cbdcconfig.Save(ctx, cfg))

	// The file landed at the override path...
	require.FileExists(t, overridePath)
	// ...and NOT at the default location.
	require.NoFileExists(t, filepath.Join(home, ".cbdinocluster"))

	loaded, err := cbdcconfig.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, "isolated-net", loaded.Docker.Network)

	// Sanity: a stray default-location file is never consulted while the
	// override is active.
	require.NoError(t, os.WriteFile(filepath.Join(home, ".cbdinocluster"), []byte("version: 6\ndocker:\n  network: default-net\n"), 0600))
	loaded, err = cbdcconfig.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, "isolated-net", loaded.Docker.Network)
}

// TestSaveFailsWhenConfigDirCannotBeCreated is the defensive counterpart to the
// directory-creating Save: when the parent directory cannot be created (here a
// regular file sits where Save needs a directory), Save must surface an error
// rather than silently swallow the config. Defends the MkdirAll error path.
func TestSaveFailsWhenConfigDirCannotBeCreated(t *testing.T) {
	ctx := context.Background()
	setHomeDir(t, t.TempDir())
	t.Setenv(cbdcconfig.EnvConfigPath, "")

	// A regular file occupies the spot where Save wants to create a directory,
	// so MkdirAll(filepath.Dir(overridePath)) is forced to fail on every OS.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0600))

	overridePath := filepath.Join(blocker, "sub", "config.yaml")
	cbdcconfig.SetConfigPathOverride(overridePath)
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })

	err := cbdcconfig.Save(ctx, &cbdcconfig.Config{Version: cbdcconfig.Version})
	require.Error(t, err)
	require.NoFileExists(t, overridePath)
}
