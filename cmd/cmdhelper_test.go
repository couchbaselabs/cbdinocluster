package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeDeployer struct {
	deployment.Deployer
	clusters    []deployment.ClusterInfo
	err         error
	waitForDone bool
}

func (d *fakeDeployer) FindClusters(ctx context.Context, idPrefix string) ([]deployment.ClusterInfo, error) {
	if d.waitForDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return d.clusters, d.err
}

type fakeCluster struct {
	deployment.ClusterInfo
}

func TestFindCluster(t *testing.T) {
	cluster := &fakeCluster{}
	listErr := errors.New("list failed")

	t.Run("found while another deployer fails", func(t *testing.T) {
		name, _, found, err := findCluster(context.Background(), zap.NewNop(), map[string]deployment.Deployer{
			"cloud":  &fakeDeployer{clusters: []deployment.ClusterInfo{cluster}},
			"docker": &fakeDeployer{err: listErr},
		}, "c1")
		require.NoError(t, err)
		assert.Equal(t, "cloud", name)
		assert.Same(t, cluster, found)
	})

	t.Run("every deployer listed and none has it", func(t *testing.T) {
		_, _, _, err := findCluster(context.Background(), zap.NewNop(), map[string]deployment.Deployer{
			"cloud":  &fakeDeployer{},
			"docker": &fakeDeployer{},
		}, "c1")
		require.ErrorIs(t, err, errClusterNotFound)
	})

	t.Run("a deployer failed to list", func(t *testing.T) {
		_, _, _, err := findCluster(context.Background(), zap.NewNop(), map[string]deployment.Deployer{
			"cloud":  &fakeDeployer{err: listErr},
			"docker": &fakeDeployer{},
		}, "c1")
		require.ErrorIs(t, err, errClusterLookupFailed)
		assert.NotContains(t, err.Error(), "failed to identify cluster")
	})

	t.Run("the lookup hit the deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()

		_, _, _, err := findCluster(ctx, zap.NewNop(), map[string]deployment.Deployer{
			"cloud":  &fakeDeployer{waitForDone: true},
			"docker": &fakeDeployer{},
		}, "c1")
		require.ErrorIs(t, err, errClusterLookupFailed)
	})
}
