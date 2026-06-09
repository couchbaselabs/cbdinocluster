package deployment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseBucketType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    BucketType
		wantErr bool
	}{
		{name: "empty defaults to couchbase", input: "", want: BucketTypeCouchbase},
		{name: "whitespace-only defaults to couchbase", input: "   ", want: BucketTypeCouchbase},
		{name: "couchbase", input: "couchbase", want: BucketTypeCouchbase},
		{name: "membase alias", input: "membase", want: BucketTypeCouchbase},
		{name: "ephemeral", input: "ephemeral", want: BucketTypeEphemeral},
		{name: "memcached", input: "memcached", want: BucketTypeMemcached},
		{name: "uppercase is normalized", input: "EPHEMERAL", want: BucketTypeEphemeral},
		{name: "surrounding whitespace is trimmed", input: "  memcached  ", want: BucketTypeMemcached},
		{name: "mixed case couchbase", input: "Couchbase", want: BucketTypeCouchbase},
		{name: "invalid value errors", input: "nonsense", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBucketType(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				require.Empty(t, got, "an error must return the zero-value bucket type")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseBucketTypeErrorMessageListsValidTypes(t *testing.T) {
	_, err := ParseBucketType("nonsense")
	require.Error(t, err)
	require.Contains(t, err.Error(), "couchbase")
	require.Contains(t, err.Error(), "ephemeral")
	require.Contains(t, err.Error(), "memcached")
}

// TestAllBucketTypesParse guards that every canonical value in AllBucketTypes
// round-trips through ParseBucketType, so the list and the parser cannot drift.
func TestAllBucketTypesParse(t *testing.T) {
	for _, bt := range AllBucketTypes {
		t.Run(string(bt), func(t *testing.T) {
			got, err := ParseBucketType(string(bt))
			require.NoError(t, err)
			require.Equal(t, bt, got)
		})
	}
}
