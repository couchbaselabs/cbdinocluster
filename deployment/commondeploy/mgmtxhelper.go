package commondeploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/couchbase/gocbcorex/cbmgmtx"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/pkg/errors"
)

type MgmtxHelper struct {
	Mgmt *cbmgmtx.Management
}

func (h MgmtxHelper) ListBuckets(ctx context.Context) ([]deployment.BucketInfo, error) {
	resp, err := h.Mgmt.GetAllBuckets(ctx, &cbmgmtx.GetAllBucketsOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list buckets")
	}

	var buckets []deployment.BucketInfo
	for _, bucket := range resp {
		buckets = append(buckets, deployment.BucketInfo{
			Name: bucket.Name,
		})
	}

	return buckets, nil
}

func (h MgmtxHelper) CreateBucket(ctx context.Context, opts *deployment.CreateBucketOptions) error {
	ramQuotaMb := 256
	if opts.RamQuotaMB > 0 {
		ramQuotaMb = opts.RamQuotaMB
	}

	err := h.Mgmt.CreateBucket(ctx, &cbmgmtx.CreateBucketOptions{
		BucketName: opts.Name,
		BucketSettings: cbmgmtx.BucketSettings{
			BucketType:             "membase",
			StorageBackend:         "couchstore",
			ReplicaIndex:           false,
			ConflictResolutionType: "seqno",
			MutableBucketSettings: cbmgmtx.MutableBucketSettings{
				EvictionPolicy:     "valueOnly",
				ReplicaNumber:      uint32(opts.NumReplicas),
				DurabilityMinLevel: "none",
				CompressionMode:    "",
				MaxTTL:             0,
				RAMQuotaMB:         uint64(ramQuotaMb),
				FlushEnabled:       opts.FlushEnabled,
			},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("%w: %s", deployment.ErrBucketAlreadyExists, err.Error())
		}
		return errors.Wrap(err, "failed to create bucket")
	}

	// WARNING: When this particular helper is used, we are in a situation where we cannot
	// neccessarily poll all the nodes to confirm the bucket exists...

	return nil
}

func (h MgmtxHelper) DeleteBucket(ctx context.Context, bucketName string) error {
	err := h.Mgmt.DeleteBucket(ctx, &cbmgmtx.DeleteBucketOptions{
		BucketName: bucketName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete bucket")
	}

	return nil
}

func (h MgmtxHelper) ListCollections(ctx context.Context, bucketName string) ([]deployment.ScopeInfo, error) {
	manifest, err := h.Mgmt.GetCollectionManifest(ctx, &cbmgmtx.GetCollectionManifestOptions{
		BucketName: bucketName,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch collection manifest")
	}

	var scopes []deployment.ScopeInfo
	for _, scope := range manifest.Scopes {
		var collections []deployment.CollectionInfo
		for _, collection := range scope.Collections {
			collections = append(collections, deployment.CollectionInfo{
				Name: collection.Name,
			})
		}
		scopes = append(scopes, deployment.ScopeInfo{
			Name:        scope.Name,
			Collections: collections,
		})
	}

	return scopes, nil
}

func (h MgmtxHelper) CreateScope(ctx context.Context, bucketName, scopeName string) error {
	_, err := h.Mgmt.CreateScope(ctx, &cbmgmtx.CreateScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create scope")
	}

	// WARNING: When this particular helper is used, we are in a situation where we cannot
	// neccessarily poll all the nodes to confirm the scope exists...

	return nil
}

func (h MgmtxHelper) CreateCollection(ctx context.Context, bucketName, scopeName, collectionName string) error {
	_, err := h.Mgmt.CreateCollection(ctx, &cbmgmtx.CreateCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create collection")
	}

	// WARNING: When this particular helper is used, we are in a situation where we cannot
	// neccessarily poll all the nodes to confirm the collection exists...

	return nil
}

func (h MgmtxHelper) DeleteScope(ctx context.Context, bucketName, scopeName string) error {
	_, err := h.Mgmt.DeleteScope(ctx, &cbmgmtx.DeleteScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete scope")
	}

	return nil
}

func (h MgmtxHelper) DeleteCollection(ctx context.Context, bucketName, scopeName, collectionName string) error {
	_, err := h.Mgmt.DeleteCollection(ctx, &cbmgmtx.DeleteCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete collection")
	}

	return nil
}
