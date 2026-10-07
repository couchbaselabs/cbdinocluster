package dockerdeploy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDockerNofile(t *testing.T) {
	tests := []struct {
		value string
		mode  nofileMode
		limit int64
	}{
		{"", nofileModeAuto, DefaultDockerNofile},
		{"auto", nofileModeAuto, DefaultDockerNofile},
		{" AUTO ", nofileModeAuto, DefaultDockerNofile},
		{"0", nofileModeInherit, 0},
		{"none", nofileModeInherit, 0},
		{"20000", nofileModeFixed, 20000},
	}

	for _, tt := range tests {
		mode, limit, err := parseDockerNofile(tt.value)
		require.NoError(t, err, tt.value)
		require.Equal(t, tt.mode, mode, tt.value)
		require.Equal(t, tt.limit, limit, tt.value)
	}

	for _, value := range []string{"-1", "lots", "1.5"} {
		_, _, err := parseDockerNofile(value)
		require.Error(t, err, value)
	}
}

func TestIsRlimitError(t *testing.T) {
	require.True(t, isRlimitError(errors.New(
		"failed to create task for container: failed to create shim task: OCI runtime create failed: "+
			"runc create failed: unable to start container process: error during container init: "+
			"error setting rlimits for ready process: error setting rlimit type 7: operation not permitted")))
	require.False(t, isRlimitError(errors.New("no such image")))
	require.False(t, isRlimitError(nil))
}
