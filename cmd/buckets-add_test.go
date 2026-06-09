package cmd

import (
	"testing"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/stretchr/testify/require"
)

// TestBucketsAddBucketTypeFlag guards the CLI surface for the bucket type:
// the flag must exist and default to "couchbase" so that existing invocations
// (which never passed a type) keep creating couchbase buckets.
func TestBucketsAddBucketTypeFlag(t *testing.T) {
	flag := bucketsAddCmd.Flags().Lookup("bucket-type")
	require.NotNil(t, flag, "buckets add must expose a --bucket-type flag")
	require.Equal(t, "couchbase", flag.DefValue)
}

// TestNewCreateBucketOptionsWiring asserts that the parsed bucket type and the
// remaining bucket settings actually land in the CreateBucketOptions handed to
// the deployer. This is the glue between the CLI flags and the deployer that
// the flag-default test alone does not cover.
func TestNewCreateBucketOptionsWiring(t *testing.T) {
	tests := []struct {
		name           string
		bucketTypeStr  string
		wantBucketType deployment.BucketType
	}{
		{name: "flag default", bucketTypeStr: "couchbase", wantBucketType: deployment.BucketTypeCouchbase},
		{name: "empty defaults to couchbase", bucketTypeStr: "", wantBucketType: deployment.BucketTypeCouchbase},
		{name: "ephemeral", bucketTypeStr: "ephemeral", wantBucketType: deployment.BucketTypeEphemeral},
		{name: "memcached", bucketTypeStr: "memcached", wantBucketType: deployment.BucketTypeMemcached},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := newCreateBucketOptions("mybucket", tt.bucketTypeStr, 256, true, 2)
			require.NoError(t, err)
			require.Equal(t, "mybucket", opts.Name)
			require.Equal(t, tt.wantBucketType, opts.BucketType)
			require.Equal(t, 256, opts.RamQuotaMB)
			require.True(t, opts.FlushEnabled)
			require.Equal(t, 2, opts.NumReplicas)
		})
	}
}

func TestNewCreateBucketOptionsInvalidType(t *testing.T) {
	opts, err := newCreateBucketOptions("mybucket", "nonsense", 0, false, 0)
	require.Error(t, err)
	require.Nil(t, opts)
}

// TestBucketsAddFlagDefaultIsValid ties the flag default to the parser: the
// default string must be a bucket type the option-builder accepts, so the
// zero-config path can never fail validation in the command's Run.
func TestBucketsAddFlagDefaultIsValid(t *testing.T) {
	flag := bucketsAddCmd.Flags().Lookup("bucket-type")
	require.NotNil(t, flag)

	opts, err := newCreateBucketOptions("b", flag.DefValue, 0, false, 0)
	require.NoError(t, err)
	require.Equal(t, deployment.BucketTypeCouchbase, opts.BucketType)
}
