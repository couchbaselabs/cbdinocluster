package commondeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/couchbase/gocbcorex"
	"github.com/couchbase/gocbcorex/cbmgmtx"
	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/pkg/errors"
)

type ClusterHelper struct {
	Agent *gocbcorex.Agent
}

func (h ClusterHelper) ListBuckets(ctx context.Context) ([]deployment.BucketInfo, error) {
	resp, err := h.Agent.GetAllBuckets(ctx, &cbmgmtx.GetAllBucketsOptions{})
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

func (h ClusterHelper) CreateBucket(ctx context.Context, opts *deployment.CreateBucketOptions) error {
	ramQuotaMb := 256
	if opts.RamQuotaMB > 0 {
		ramQuotaMb = opts.RamQuotaMB
	}

	err := h.Agent.CreateBucket(ctx, &cbmgmtx.CreateBucketOptions{
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

	err = h.Agent.EnsureBucket(ctx, &gocbcorex.EnsureBucketOptions{
		BucketName:  opts.Name,
		WantHealthy: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bucket")
	}

	return nil
}

func (h ClusterHelper) DeleteBucket(ctx context.Context, bucketName string) error {
	err := h.Agent.DeleteBucket(ctx, &cbmgmtx.DeleteBucketOptions{
		BucketName: bucketName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete bucket")
	}

	err = h.Agent.EnsureBucket(ctx, &gocbcorex.EnsureBucketOptions{
		BucketName:  bucketName,
		WantMissing: true,
	})
	if err != nil {
		return errors.Wrap(err, "failed to ensure bucket")
	}

	return nil
}

func (h ClusterHelper) ExecuteQuery(ctx context.Context, query string) (string, error) {
	results, err := h.Agent.Query(ctx, &gocbcorex.QueryOptions{
		Statement: query,
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to execute query")
	}

	rows := make([]json.RawMessage, 0)
	for results.HasMoreRows() {
		row, err := results.ReadRow()
		if err != nil {
			return "", errors.Wrap(err, "failed to read row")
		}

		rows = append(rows, row)
	}

	rowsBytes, err := json.Marshal(rows)
	if err != nil {
		return "", errors.Wrap(err, "failed to serialize rows")
	}

	return string(rowsBytes), nil
}

func (h ClusterHelper) ListCollections(ctx context.Context, bucketName string) ([]deployment.ScopeInfo, error) {
	manifest, err := h.Agent.GetCollectionManifest(ctx, &cbmgmtx.GetCollectionManifestOptions{
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

func (h ClusterHelper) CreateScope(ctx context.Context, bucketName, scopeName string) error {
	_, err := h.Agent.CreateScope(ctx, &cbmgmtx.CreateScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create scope")
	}

	return nil
}

func (h ClusterHelper) CreateCollection(ctx context.Context, bucketName, scopeName, collectionName string) error {
	_, err := h.Agent.CreateCollection(ctx, &cbmgmtx.CreateCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create collection")
	}

	return nil
}

func (h ClusterHelper) DeleteScope(ctx context.Context, bucketName, scopeName string) error {
	_, err := h.Agent.DeleteScope(ctx, &cbmgmtx.DeleteScopeOptions{
		BucketName: bucketName,
		ScopeName:  scopeName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete scope")
	}

	return nil
}

func (h ClusterHelper) DeleteCollection(ctx context.Context, bucketName, scopeName, collectionName string) error {
	_, err := h.Agent.DeleteCollection(ctx, &cbmgmtx.DeleteCollectionOptions{
		BucketName:     bucketName,
		ScopeName:      scopeName,
		CollectionName: collectionName,
	})
	if err != nil {
		return errors.Wrap(err, "failed to delete collection")
	}

	return nil
}
