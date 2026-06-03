package cbdcconfig_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/stretchr/testify/require"
)

func TestConfigPathDefault(t *testing.T) {
	cbdcconfig.SetConfigPathOverride("")
	t.Setenv(cbdcconfig.EnvConfigPath, "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := cbdcconfig.ConfigPath()
	require.NoError(t, err)
	require.Equal(t, path.Join(home, ".cbdinocluster"), got)
}

func TestConfigPathFromEnv(t *testing.T) {
	cbdcconfig.SetConfigPathOverride("")
	t.Setenv("HOME", t.TempDir())
	envPath := path.Join(t.TempDir(), "from-env.yaml")
	t.Setenv(cbdcconfig.EnvConfigPath, envPath)

	got, err := cbdcconfig.ConfigPath()
	require.NoError(t, err)
	require.Equal(t, envPath, got)
}

func TestConfigPathOverrideBeatsEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(cbdcconfig.EnvConfigPath, path.Join(t.TempDir(), "from-env.yaml"))
	overridePath := path.Join(t.TempDir(), "from-flag.yaml")
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
	t.Setenv("HOME", home)
	t.Setenv(cbdcconfig.EnvConfigPath, "")

	overridePath := path.Join(t.TempDir(), "isolated.yaml")
	cbdcconfig.SetConfigPathOverride(overridePath)
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })

	cfg := &cbdcconfig.Config{Version: cbdcconfig.Version}
	cfg.Docker.Enabled.Set(true)
	cfg.Docker.Network = "isolated-net"
	require.NoError(t, cbdcconfig.Save(ctx, cfg))

	// The file landed at the override path...
	require.FileExists(t, overridePath)
	// ...and NOT at the default location.
	require.NoFileExists(t, path.Join(home, ".cbdinocluster"))

	loaded, err := cbdcconfig.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, "isolated-net", loaded.Docker.Network)

	// Sanity: a stray default-location file is never consulted while the
	// override is active.
	require.NoError(t, os.WriteFile(path.Join(home, ".cbdinocluster"), []byte("version: 6\ndocker:\n  network: default-net\n"), 0600))
	loaded, err = cbdcconfig.Load(ctx)
	require.NoError(t, err)
	require.Equal(t, "isolated-net", loaded.Docker.Network)
}
