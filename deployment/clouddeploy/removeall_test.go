package clouddeploy

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupShouldTake(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour)
	live := now.Add(time.Hour)

	tests := []struct {
		name string
		meta stringclustermeta.MetaData
		opts deployment.CleanupOptions
		want bool
	}{
		{
			name: "takes the expired",
			meta: stringclustermeta.MetaData{Expiry: expired},
			want: true,
		},
		{
			name: "expiry equal to now counts as expired",
			meta: stringclustermeta.MetaData{Expiry: now},
			want: true,
		},
		{
			name: "keeps the live",
			meta: stringclustermeta.MetaData{Expiry: live},
			want: false,
		},
		{
			name: "keeps the never expiring",
			meta: stringclustermeta.MetaData{},
			want: false,
		},
		{
			name: "purpose scope takes the expired match",
			meta: stringclustermeta.MetaData{Purpose: "sdk-nightly", Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: true,
		},
		{
			name: "purpose scope keeps the live match",
			meta: stringclustermeta.MetaData{Purpose: "sdk", Expiry: live},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: false,
		},
		{
			name: "purpose scope keeps the expired mismatch",
			meta: stringclustermeta.MetaData{Purpose: "sdkxyz", Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: false,
		},
		{
			name: "purpose scope keeps an expired empty purpose",
			meta: stringclustermeta.MetaData{Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk"},
			want: false,
		},
		{
			name: "dry run does not change the scope",
			meta: stringclustermeta.MetaData{Purpose: "perf", Expiry: expired},
			opts: deployment.CleanupOptions{Purpose: "sdk", DryRun: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cleanupShouldTake(&tt.meta, tt.opts, now))
		})
	}
}

func TestSkipReason(t *testing.T) {
	tests := []struct {
		name  string
		state string
		skip  bool
		want  string
	}{
		{name: "cleanup skips destroyFailed", state: capellav4.StateDestroyFailed, skip: true,
			want: "skipping cluster in destroyFailed state, it needs manual removal"},
		{name: "cleanup skips destroying", state: capellav4.StateDestroying, skip: true,
			want: "skipping expired cluster in destroying state"},
		{name: "cleanup takes healthy", state: capellav4.StateHealthy, skip: true, want: ""},
		{name: "cleanup takes an unknown state", state: "", skip: true, want: ""},
		{name: "remove-all takes destroyFailed", state: capellav4.StateDestroyFailed, skip: false, want: ""},
		{name: "remove-all takes destroying", state: capellav4.StateDestroying, skip: false, want: ""},
		{name: "remove-all takes healthy", state: capellav4.StateHealthy, skip: false, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, skipReason(tt.state, tt.skip))
		})
	}
}

// newSingleClusterHandler serves one project that holds one cloud cluster in
// the given state. Any other call fails the test, so a cleanup that tries a
// delete is caught.
func newSingleClusterHandler(t *testing.T, projectID, clusterID, state string) http.Handler {
	t.Helper()

	writeJson := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(body))
	}

	projectPath := "/v4/organizations/" + testTenantID + "/projects/" + projectID

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+projectPath+"/clusters", func(w http.ResponseWriter, _ *http.Request) {
		writeJson(w, map[string]any{"data": []any{
			map[string]any{"id": clusterID, "currentState": state},
		}})
	})
	mux.HandleFunc("GET "+projectPath+"/analyticsClusters", func(w http.ResponseWriter, _ *http.Request) {
		writeJson(w, map[string]any{})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func TestListRemovalTargetsDestroying(t *testing.T) {
	deployer := newTestDeployer(t, newSingleClusterHandler(t, "p-1", "c-1", capellav4.StateDestroying))

	targets, keepReason, err := deployer.listRemovalTargets(context.Background(), "p-1", true)
	require.NoError(t, err)
	assert.Empty(t, targets, "cleanup must not delete or wait on a destroying cluster")
	assert.Equal(t, "skipping expired cluster in destroying state", keepReason)

	targets, keepReason, err = deployer.listRemovalTargets(context.Background(), "p-1", false)
	require.NoError(t, err)
	assert.Equal(t, []removalTarget{{projectID: "p-1", clusterID: "c-1"}}, targets,
		"remove-all must still take a destroying cluster")
	assert.Empty(t, keepReason)
}

// A wait on a cluster whose delete failed runs until the deadline. The handler
// fails the test if the wait polls that cluster.
func TestRemoveTargetsSkipsWaitOnFailedDelete(t *testing.T) {
	projectsPath := "/v4/organizations/" + testTenantID + "/projects"
	failDelete := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	var waitedOnDeleted atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE "+projectsPath+"/p-1/clusters/c-bad", failDelete)
	mux.HandleFunc("DELETE "+projectsPath+"/p-1/clusters/freeTier/c-bad", failDelete)
	mux.HandleFunc("DELETE "+projectsPath+"/p-2/clusters/c-ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET "+projectsPath+"/p-2/clusters/c-ok", func(w http.ResponseWriter, _ *http.Request) {
		waitedOnDeleted.Store(true)
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})

	deployer := newTestDeployer(t, mux)

	failedProjects, err := deployer.removeTargets(context.Background(), []removalTarget{
		{projectID: "p-1", clusterID: "c-bad"},
		{projectID: "p-2", clusterID: "c-ok"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to remove cluster")
	assert.Equal(t, map[string]bool{"p-1": true}, failedProjects)
	assert.True(t, waitedOnDeleted.Load(), "the cluster whose delete worked must still be waited on")
}

// A cleanup keeps the project of a destroying cluster, as Capella refuses to
// delete a project that still holds a cluster. The handler fails the test on
// any delete call.
func TestRemoveProjectsKeepsProjectOfDestroyingCluster(t *testing.T) {
	deployer := newTestDeployer(t, newSingleClusterHandler(t, "p-1", "c-1", capellav4.StateDestroying))

	meta := stringclustermeta.MetaData{ID: cbdcuuid.New(), Expiry: time.Now().Add(-time.Hour)}
	projects := []cbdc2Project{{
		Meta: &meta,
		Info: &capellav4.ProjectInfo{ID: "p-1", Name: meta.String()},
	}}

	require.NoError(t, deployer.removeProjects(context.Background(), projects, true))

	removed, kept, err := deployer.dryRunRemoveProjects(context.Background(), projects, true, "expired")
	require.NoError(t, err)
	assert.Equal(t, 0, removed)
	assert.Equal(t, 1, kept)
}

// Every project delete goes through the ownership guard, so it must accept
// every name cbdinocluster generates and refuse everything else.
func TestCanDeleteProjectName(t *testing.T) {
	ownedMeta := stringclustermeta.MetaData{
		ID:     cbdcuuid.New(),
		Expiry: time.Now().Add(time.Hour),
	}
	assert.True(t, canDeleteProjectName(ownedMeta.String()))

	ownedMeta.Purpose = "sdk-nightly"
	assert.True(t, canDeleteProjectName(ownedMeta.String()))

	assert.False(t, canDeleteProjectName(""))
	assert.False(t, canDeleteProjectName("shared-team-project"))
	assert.False(t, canDeleteProjectName("cbdc2_notauuid_20260101-000000"))
	assert.False(t, canDeleteProjectName("cbdc2_onlyoneextrapart"))
}
