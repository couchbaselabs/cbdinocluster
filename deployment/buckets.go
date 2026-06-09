package deployment

import (
	"fmt"
	"strings"
)

// BucketType identifies which kind of Couchbase bucket to create. The values
// are the canonical, user-facing names; ParseBucketType maps the various
// aliases (and the server-internal "membase" name) onto these.
type BucketType string

const (
	// BucketTypeCouchbase is a persistent, replicated bucket (the server
	// internally calls this "membase"). It is the default.
	BucketTypeCouchbase BucketType = "couchbase"
	// BucketTypeEphemeral is a memory-only replicated bucket with no disk
	// persistence.
	BucketTypeEphemeral BucketType = "ephemeral"
	// BucketTypeMemcached is a non-replicated, memory-only cache bucket. It is
	// a legacy type that Couchbase Server 8.0 and later reject; it is only
	// creatable on older clusters.
	BucketTypeMemcached BucketType = "memcached"
)

// AllBucketTypes lists every supported bucket type. It is the single list each
// deployer's per-type mapping is expected to handle; tests iterate it to catch
// a newly-added type that a deployer forgot to map.
var AllBucketTypes = []BucketType{
	BucketTypeCouchbase,
	BucketTypeEphemeral,
	BucketTypeMemcached,
}

// ParseBucketType normalizes a user-supplied bucket type string into a
// BucketType. An empty string defaults to BucketTypeCouchbase. Both the
// user-facing "couchbase" and the server-internal "membase" names are
// accepted. It returns an error for any unrecognized value.
func ParseBucketType(s string) (BucketType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "couchbase", "membase":
		return BucketTypeCouchbase, nil
	case "ephemeral":
		return BucketTypeEphemeral, nil
	case "memcached":
		return BucketTypeMemcached, nil
	default:
		return "", fmt.Errorf("invalid bucket type %q (valid types: couchbase, ephemeral, memcached)", s)
	}
}
