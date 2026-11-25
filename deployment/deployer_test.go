package deployment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPurposeMatches(t *testing.T) {
	tests := []struct {
		name    string
		purpose string
		scope   string
		want    bool
	}{
		{
			name:    "no scope takes everything",
			purpose: "sdk-nightly",
			want:    true,
		},
		{
			name:    "no scope takes an empty purpose",
			purpose: "",
			want:    true,
		},
		{
			name:    "equal purpose matches",
			purpose: "sdk",
			scope:   "sdk",
			want:    true,
		},
		{
			name:    "purpose followed by a dash matches",
			purpose: "sdk-nightly",
			scope:   "sdk",
			want:    true,
		},
		{
			name:    "purpose followed by other text is kept",
			purpose: "sdkxyz",
			scope:   "sdk",
			want:    false,
		},
		{
			name:    "different purpose is kept",
			purpose: "perf",
			scope:   "sdk",
			want:    false,
		},
		{
			name:    "empty purpose never matches a scope",
			purpose: "",
			scope:   "sdk",
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, PurposeMatches(test.purpose, test.scope))
		})
	}
}
