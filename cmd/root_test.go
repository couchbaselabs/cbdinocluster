package cmd

import (
	"testing"

	"github.com/couchbaselabs/cbdinocluster/cbdcconfig"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestConfigFlagSurvivesSubcommandPreRun guards against the cobra
// PersistentPreRunE shadowing footgun: by default cobra runs only the most
// specific persistent pre-run hook, so a subcommand defining its own would
// silently suppress the root's --config resolution. This test attaches such a
// subcommand and asserts the override is still applied — which only holds
// because cobra.EnableTraverseRunHooks is enabled in init(). Flip that off and
// this test fails, surfacing the regression instead of leaving a latent bug.
func TestConfigFlagSurvivesSubcommandPreRun(t *testing.T) {
	cbdcconfig.SetConfigPathOverride("")
	t.Cleanup(func() { cbdcconfig.SetConfigPathOverride("") })

	childRan := false
	child := &cobra.Command{
		Use: "dummychild",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			childRan = true
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	rootCmd.AddCommand(child)
	t.Cleanup(func() {
		rootCmd.RemoveCommand(child)
		rootCmd.SetArgs(nil)
	})

	const overridePath = "/tmp/cbdinocluster-test-explicit-config.yaml"
	rootCmd.SetArgs([]string{"dummychild", "--config", overridePath})
	require.NoError(t, rootCmd.Execute())

	require.True(t, childRan, "the subcommand's own PersistentPreRunE should have run")

	got, err := cbdcconfig.ConfigPath()
	require.NoError(t, err)
	require.Equal(t, overridePath, got,
		"root --config resolution must run even when a subcommand defines its own PersistentPreRunE")
}
