package commondeploy

import (
	"testing"

	"github.com/couchbase/gocbcorex/cbmgmtx"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/stretchr/testify/require"
)

func TestBuildMgmtxCreateBucketOptionsCouchbase(t *testing.T) {
	got, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
		Name:         "default",
		BucketType:   deployment.BucketTypeCouchbase,
		RamQuotaMB:   512,
		FlushEnabled: true,
		NumReplicas:  2,
	})
	require.NoError(t, err)

	require.Equal(t, "default", got.BucketName)
	require.Equal(t, cbmgmtx.BucketTypeCouchbase, got.BucketSettings.BucketType)
	require.Equal(t, cbmgmtx.StorageBackendCouchstore, got.BucketSettings.StorageBackend)
	require.Equal(t, cbmgmtx.EvictionPolicyTypeValueOnly, got.BucketSettings.EvictionPolicy)
	require.Equal(t, cbmgmtx.ConflictResolutionTypeSequenceNumber, got.BucketSettings.ConflictResolutionType)
	require.False(t, got.BucketSettings.ReplicaIndex)
	require.Equal(t, uint32(2), got.BucketSettings.ReplicaNumber)
	require.Equal(t, uint64(512), got.BucketSettings.RAMQuotaMB)
	require.True(t, got.BucketSettings.FlushEnabled)
}

func TestBuildMgmtxCreateBucketOptionsEmptyTypeDefaultsToCouchbase(t *testing.T) {
	got, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
		Name: "default",
	})
	require.NoError(t, err)

	require.Equal(t, cbmgmtx.BucketTypeCouchbase, got.BucketSettings.BucketType)
	require.Equal(t, cbmgmtx.StorageBackendCouchstore, got.BucketSettings.StorageBackend)
	require.Equal(t, cbmgmtx.EvictionPolicyTypeValueOnly, got.BucketSettings.EvictionPolicy)
}

func TestBuildMgmtxCreateBucketOptionsDefaultRAMQuota(t *testing.T) {
	got, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
		Name: "default",
	})
	require.NoError(t, err)

	require.Equal(t, uint64(defaultBucketRAMQuotaMB), got.BucketSettings.RAMQuotaMB)
}

func TestBuildMgmtxCreateBucketOptionsEphemeral(t *testing.T) {
	got, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
		Name:        "events",
		BucketType:  deployment.BucketTypeEphemeral,
		NumReplicas: 1,
	})
	require.NoError(t, err)

	require.Equal(t, cbmgmtx.BucketTypeEphemeral, got.BucketSettings.BucketType)
	// Ephemeral buckets are memory-only: no disk storage backend may be set,
	// and only noEviction/nruEviction are valid eviction policies.
	require.Equal(t, cbmgmtx.StorageBackendUnset, got.BucketSettings.StorageBackend)
	require.Equal(t, cbmgmtx.EvictionPolicyTypeNoEviction, got.BucketSettings.EvictionPolicy)
	require.False(t, got.BucketSettings.ReplicaIndex)
	require.Equal(t, uint32(1), got.BucketSettings.ReplicaNumber)
}

func TestBuildMgmtxCreateBucketOptionsMemcached(t *testing.T) {
	got, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
		Name:        "cache",
		BucketType:  deployment.BucketTypeMemcached,
		NumReplicas: 2,
	})
	require.NoError(t, err)

	require.Equal(t, cbmgmtx.BucketTypeMemcached, got.BucketSettings.BucketType)
	// Memcached buckets are a flat in-memory cache: no replicas, no conflict
	// resolution, no durability, no eviction policy and no storage backend.
	// Every one of these must be left unset so the encoder omits it from the
	// request the server would otherwise reject.
	require.Equal(t, uint32(0), got.BucketSettings.ReplicaNumber)
	require.Equal(t, cbmgmtx.ConflictResolutionTypeUnset, got.BucketSettings.ConflictResolutionType)
	require.Equal(t, cbmgmtx.DurabilityLevelUnset, got.BucketSettings.DurabilityMinLevel)
	require.Equal(t, cbmgmtx.EvictionPolicyTypeUnset, got.BucketSettings.EvictionPolicy)
	require.Equal(t, cbmgmtx.StorageBackendUnset, got.BucketSettings.StorageBackend)
}

func TestBuildMgmtxCreateBucketOptionsUnsupportedTypeErrors(t *testing.T) {
	got, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
		Name:       "broken",
		BucketType: deployment.BucketType("nonsense"),
	})
	require.Error(t, err)
	require.Nil(t, got)
}

// TestBuildMgmtxCreateBucketOptionsHandlesAllTypes is a drift guard: every
// bucket type in deployment.AllBucketTypes must be handled by the builder
// without hitting the unsupported-type error path.
func TestBuildMgmtxCreateBucketOptionsHandlesAllTypes(t *testing.T) {
	for _, bt := range deployment.AllBucketTypes {
		t.Run(string(bt), func(t *testing.T) {
			_, err := buildMgmtxCreateBucketOptions(&deployment.CreateBucketOptions{
				Name:       "b",
				BucketType: bt,
			})
			require.NoError(t, err)
		})
	}
}
