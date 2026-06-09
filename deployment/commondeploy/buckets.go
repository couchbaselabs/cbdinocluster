package commondeploy

import (
	"github.com/couchbase/gocbcorex/cbmgmtx"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/pkg/errors"
)

// defaultBucketRAMQuotaMB is used when the caller does not request a specific
// RAM quota for the bucket.
const defaultBucketRAMQuotaMB = 256

// buildMgmtxCreateBucketOptions translates the deployment-level bucket creation
// options into the cbmgmtx settings used against a live server, applying the
// per-bucket-type rules that Couchbase Server enforces:
//
//   - couchbase ("membase"): persistent, couchstore storage backend, valueOnly
//     eviction.
//   - ephemeral: memory-only, so no disk storage backend may be specified and
//     only the noEviction/nruEviction policies are valid.
//   - memcached: a legacy, flat in-memory cache with no replicas, no conflict
//     resolution, no durability, no eviction policy and no storage backend.
//     Couchbase Server 8.0+ rejects this type outright ("memcached buckets are
//     no longer supported"); it is only creatable on older clusters.
//
// It returns an error for an unrecognized bucket type rather than silently
// falling back to a couchbase bucket, so an invalid type that bypassed
// ParseBucketType (e.g. a directly-constructed CreateBucketOptions) fails
// closed.
func buildMgmtxCreateBucketOptions(opts *deployment.CreateBucketOptions) (*cbmgmtx.CreateBucketOptions, error) {
	ramQuotaMB := defaultBucketRAMQuotaMB
	if opts.RamQuotaMB > 0 {
		ramQuotaMB = opts.RamQuotaMB
	}

	bucketType := opts.BucketType
	if bucketType == "" {
		bucketType = deployment.BucketTypeCouchbase
	}

	settings := cbmgmtx.BucketSettings{
		ConflictResolutionType: cbmgmtx.ConflictResolutionTypeSequenceNumber,
		ReplicaIndex:           false,
		MutableBucketSettings: cbmgmtx.MutableBucketSettings{
			ReplicaNumber:      uint32(opts.NumReplicas),
			DurabilityMinLevel: cbmgmtx.DurabilityLevelNone,
			RAMQuotaMB:         uint64(ramQuotaMB),
			FlushEnabled:       opts.FlushEnabled,
		},
	}

	switch bucketType {
	case deployment.BucketTypeCouchbase:
		settings.BucketType = cbmgmtx.BucketTypeCouchbase
		settings.StorageBackend = cbmgmtx.StorageBackendCouchstore
		settings.EvictionPolicy = cbmgmtx.EvictionPolicyTypeValueOnly
	case deployment.BucketTypeEphemeral:
		settings.BucketType = cbmgmtx.BucketTypeEphemeral
		// Ephemeral buckets keep no data on disk; the server rejects a
		// storage backend and only accepts the noEviction/nruEviction
		// policies, so leave the storage backend unset and default to
		// noEviction.
		settings.EvictionPolicy = cbmgmtx.EvictionPolicyTypeNoEviction
	case deployment.BucketTypeMemcached:
		settings.BucketType = cbmgmtx.BucketTypeMemcached
		// Memcached buckets are a legacy, flat in-memory cache: they support
		// no replicas, no conflict resolution, no durability, no eviction
		// policy and no storage backend. Explicitly clear every setting an
		// (older) server would reject for memcached rather than relying on a
		// switch fall-through, so the intent is local and refactor-safe.
		// Couchbase Server 8.0+ rejects this type regardless of these settings.
		settings.ReplicaNumber = 0
		settings.ConflictResolutionType = cbmgmtx.ConflictResolutionTypeUnset
		settings.DurabilityMinLevel = cbmgmtx.DurabilityLevelUnset
		settings.EvictionPolicy = cbmgmtx.EvictionPolicyTypeUnset
		settings.StorageBackend = cbmgmtx.StorageBackendUnset
	default:
		return nil, errors.Errorf("unsupported bucket type %q", bucketType)
	}

	return &cbmgmtx.CreateBucketOptions{
		BucketName:     opts.Name,
		BucketSettings: settings,
	}, nil
}
