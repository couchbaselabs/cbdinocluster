package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyPurposePrefix(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		purpose  string
		expected string
	}{
		{
			name:     "no prefix keeps the purpose",
			prefix:   "",
			purpose:  "FIT-situational-cbDino",
			expected: "FIT-situational-cbDino",
		},
		{
			name:     "no purpose gives just the prefix",
			prefix:   "fitcli-run42-embev",
			purpose:  "",
			expected: "fitcli-run42-embev",
		},
		{
			name:     "prefix goes in front of the purpose",
			prefix:   "fitcli-run42-embev",
			purpose:  "FIT-situational-cbDino",
			expected: "fitcli-run42-embev-FIT-situational-cbDino",
		},
		{
			name:     "a purpose equal to the prefix is not doubled",
			prefix:   "fitcli-run42-embev",
			purpose:  "fitcli-run42-embev",
			expected: "fitcli-run42-embev",
		},
		{
			name:     "a purpose that starts with the prefix is not doubled",
			prefix:   "fitcli-run42-embev",
			purpose:  "fitcli-run42-embev-FIT-situational-cbDino",
			expected: "fitcli-run42-embev-FIT-situational-cbDino",
		},
		{
			name:     "a purpose that only shares the first characters still gets the prefix",
			prefix:   "fitcli-run42-embev",
			purpose:  "fitcli-run42-embevx",
			expected: "fitcli-run42-embev-fitcli-run42-embevx",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, applyPurposePrefix(test.prefix, test.purpose))
		})
	}
}
