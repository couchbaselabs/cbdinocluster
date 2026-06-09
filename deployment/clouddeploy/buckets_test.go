package clouddeploy

import (
	"testing"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/stretchr/testify/require"
)

func TestCapellaBucketParams(t *testing.T) {
	tests := []struct {
		name               string
		bucketType         deployment.BucketType
		wantType           string
		wantStorageBackend string
		wantErr            bool
	}{
		{
			name:               "empty defaults to couchbase",
			bucketType:         "",
			wantType:           "couchbase",
			wantStorageBackend: "couchstore",
		},
		{
			name:               "couchbase",
			bucketType:         deployment.BucketTypeCouchbase,
			wantType:           "couchbase",
			wantStorageBackend: "couchstore",
		},
		{
			// Ephemeral must report an empty storage backend so the request
			// omits it (the Capella API rejects a disk backend for ephemeral).
			name:               "ephemeral clears storage backend",
			bucketType:         deployment.BucketTypeEphemeral,
			wantType:           "ephemeral",
			wantStorageBackend: "",
		},
		{
			name:       "memcached is rejected",
			bucketType: deployment.BucketTypeMemcached,
			wantErr:    true,
		},
		{
			name:       "unknown type is rejected",
			bucketType: deployment.BucketType("nonsense"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capellaType, storageBackend, err := capellaBucketParams(tt.bucketType)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantType, capellaType)
			require.Equal(t, tt.wantStorageBackend, storageBackend)
		})
	}
}

func TestCapellaBucketParamsMemcachedMessage(t *testing.T) {
	_, _, err := capellaBucketParams(deployment.BucketTypeMemcached)
	require.Error(t, err)
	require.Contains(t, err.Error(), "memcached")
}
