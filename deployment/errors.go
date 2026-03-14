package deployment

import "errors"

// ErrBucketAlreadyExists is returned when attempting to create a bucket
// that already exists on the cluster.
var ErrBucketAlreadyExists = errors.New("bucket already exists")
