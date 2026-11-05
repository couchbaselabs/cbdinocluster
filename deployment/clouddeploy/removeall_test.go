package clouddeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/couchbaselabs/cbdinocluster/deployment"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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
func TestRemoveClustersKeepsProjectOfDestroyingCluster(t *testing.T) {
	deployer := newTestDeployer(t, newSingleClusterHandler(t, "p-1", "c-1", capellav4.StateDestroying))

	meta := stringclustermeta.MetaData{ID: cbdcuuid.New(), Expiry: time.Now().Add(-time.Hour)}
	projects := []cbdc2Project{{
		Meta: &meta,
		Info: &capellav4.ProjectInfo{ID: "p-1", Name: meta.String()},
	}}

	require.NoError(t, deployer.removeClusters(context.Background(), projects, nil, true))

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

func sharedTestCluster(cloudID string, meta stringclustermeta.MetaData, state string) *clusterInfo {
	return &clusterInfo{
		Meta:        &meta,
		ProjectID:   "p-shared",
		ProjectName: testSharedProjectName,
		Cluster:     &capellav4.ClusterInfo{ID: cloudID, Name: meta.String(), CurrentState: state},
	}
}

func cloudClusterIDs(clusters []*clusterInfo) []string {
	var out []string
	for _, cluster := range clusters {
		out = append(out, sharedCloudClusterID(cluster))
	}
	return out
}

func TestSelectSharedClusters(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Hour)
	live := now.Add(time.Hour)

	meta := func(purpose string, expiry time.Time) stringclustermeta.MetaData {
		return stringclustermeta.MetaData{ID: cbdcuuid.New(), Purpose: purpose, Expiry: expiry}
	}

	expiredColumnarMeta := meta("alice", expired)
	clusters := []*clusterInfo{
		sharedTestCluster("c-expired", meta("alice", expired), capellav4.StateHealthy),
		sharedTestCluster("c-expired-sub", meta("alice-x", expired), capellav4.StateHealthy),
		sharedTestCluster("c-expired-alicebob", meta("alicebob", expired), capellav4.StateHealthy),
		sharedTestCluster("c-expired-other", meta("bob", expired), capellav4.StateHealthy),
		sharedTestCluster("c-expired-nopurpose", meta("", expired), capellav4.StateHealthy),
		sharedTestCluster("c-live", meta("alice", live), capellav4.StateHealthy),
		sharedTestCluster("c-zero", meta("alice", time.Time{}), capellav4.StateHealthy),
		sharedTestCluster("c-failed", meta("alice", expired), capellav4.StateDestroyFailed),
		sharedTestCluster("c-failed-live", meta("alice", live), capellav4.StateDestroyFailed),
		sharedTestCluster("c-destroying", meta("alice", expired), capellav4.StateDestroying),
		sharedTestCluster("c-destroying-live", meta("alice", live), capellav4.StateDestroying),
		{
			Meta:      &expiredColumnarMeta,
			ProjectID: "p-shared",
			Columnar: &capellav4.AnalyticsClusterInfo{
				ID:           "a-expired",
				Name:         expiredColumnarMeta.String(),
				CurrentState: capellav4.StateHealthy,
			},
		},
	}

	cleanupScope := func(purpose string) func(*stringclustermeta.MetaData) bool {
		return func(meta *stringclustermeta.MetaData) bool {
			return cleanupShouldTake(meta, deployment.CleanupOptions{Purpose: purpose}, now)
		}
	}
	removeAllScope := func(purpose string) func(*stringclustermeta.MetaData) bool {
		return func(meta *stringclustermeta.MetaData) bool {
			return deployment.PurposeMatches(meta.Purpose, purpose)
		}
	}

	tests := []struct {
		name     string
		scope    func(*stringclustermeta.MetaData) bool
		skip     bool
		wantTake []string
		wantKeep []string
	}{
		{
			name:     "cleanup takes the expired match and skips destroyFailed and destroying",
			scope:    cleanupScope("alice"),
			skip:     true,
			wantTake: []string{"c-expired", "c-expired-sub", "a-expired"},
			wantKeep: []string{"c-failed", "c-destroying"},
		},
		{
			name:  "cleanup without purpose takes every expired cluster",
			scope: cleanupScope(""),
			skip:  true,
			wantTake: []string{"c-expired", "c-expired-sub", "c-expired-alicebob",
				"c-expired-other", "c-expired-nopurpose", "a-expired"},
			wantKeep: []string{"c-failed", "c-destroying"},
		},
		{
			name:  "remove-all takes the purpose match at any expiry, destroyFailed and destroying",
			scope: removeAllScope("alice"),
			skip:  false,
			wantTake: []string{"c-expired", "c-expired-sub", "c-live", "c-zero", "c-failed", "c-failed-live",
				"c-destroying", "c-destroying-live", "a-expired"},
		},
		{
			name:     "remove-all with a purpose that matches nothing",
			scope:    removeAllScope("carol"),
			skip:     false,
			wantTake: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			take, keep := selectSharedClusters(clusters, tt.scope, tt.skip)
			assert.Equal(t, tt.wantTake, cloudClusterIDs(take))
			assert.Equal(t, tt.wantKeep, cloudClusterIDs(keep))
		})
	}

	t.Run("remove-all without purpose takes every cluster", func(t *testing.T) {
		take, keep := selectSharedClusters(clusters, removeAllScope(""), false)
		assert.Len(t, take, len(clusters))
		assert.Empty(t, keep)
	})
}

type removalCluster struct {
	ID    string
	Name  string
	State string
}

// removalServer stubs the v4 calls of a removal and records the deletes. A
// cluster listing for a project with no entry in clusters fails the test, and
// so do a project create and a delete of the configured project. A deleted
// cluster answers 404, so a deletion wait ends at once.
type removalServer struct {
	t *testing.T

	mu        sync.Mutex
	projects  []capellav4.ProjectInfo
	clusters  map[string][]removalCluster
	columnars map[string][]removalCluster
	// failListing makes the cluster listing of these projects answer 403.
	failListing map[string]bool
	// missingProjects makes the cluster listing of these projects answer
	// project not found.
	missingProjects map[string]bool

	clusterDeletes []string
	projectDeletes []string
	deletes        int
}

func (s *removalServer) handler() http.Handler {
	writeJson := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		assert.NoError(s.t, json.NewEncoder(w).Encode(body))
	}
	writeNotFound := func(w http.ResponseWriter, code int) {
		writeJson(w, http.StatusNotFound, map[string]any{
			"code":           code,
			"httpStatusCode": http.StatusNotFound,
			"message":        "not found",
		})
	}
	findCluster := func(projectID, clusterID string) int {
		for i, cluster := range s.clusters[projectID] {
			if cluster.ID == clusterID {
				return i
			}
		}
		return -1
	}
	listing := func(source func() map[string][]removalCluster) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			projectID := r.PathValue("projectId")
			if s.missingProjects[projectID] {
				writeNotFound(w, 2000)
				return
			}
			if s.failListing[projectID] {
				writeJson(w, http.StatusForbidden, map[string]any{
					"code":           1002,
					"httpStatusCode": http.StatusForbidden,
					"message":        "forbidden",
				})
				return
			}
			if _, ok := s.clusters[projectID]; !ok {
				s.t.Errorf("unexpected listing %q for project %q", r.URL.Path, projectID)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			var data []any
			for _, cluster := range source()[projectID] {
				data = append(data, map[string]any{
					"id":           cluster.ID,
					"name":         cluster.Name,
					"currentState": cluster.State,
				})
			}
			writeJson(w, http.StatusOK, map[string]any{"data": data})
		}
	}

	projectsPath := "/v4/organizations/" + testTenantID + "/projects"

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+projectsPath, func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var data []any
		for _, project := range s.projects {
			data = append(data, map[string]any{"id": project.ID, "name": project.Name})
		}
		writeJson(w, http.StatusOK, map[string]any{"data": data})
	})
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/clusters",
		listing(func() map[string][]removalCluster { return s.clusters }))
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/analyticsClusters",
		listing(func() map[string][]removalCluster { return s.columnars }))
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/clusters/{clusterId}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := findCluster(r.PathValue("projectId"), r.PathValue("clusterId"))
		if i < 0 {
			writeNotFound(w, 4025)
			return
		}
		cluster := s.clusters[r.PathValue("projectId")][i]
		writeJson(w, http.StatusOK, map[string]any{"id": cluster.ID, "currentState": cluster.State})
	})
	mux.HandleFunc("DELETE "+projectsPath+"/{projectId}/clusters/{clusterId}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.deletes++
		projectID := r.PathValue("projectId")
		i := findCluster(projectID, r.PathValue("clusterId"))
		if i < 0 {
			s.t.Errorf("unexpected delete of unknown cluster %q", r.URL.Path)
			writeNotFound(w, 4025)
			return
		}
		s.clusterDeletes = append(s.clusterDeletes, projectID+"/"+r.PathValue("clusterId"))
		s.clusters[projectID] = append(s.clusters[projectID][:i], s.clusters[projectID][i+1:]...)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("DELETE "+projectsPath+"/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.deletes++
		projectID := r.PathValue("projectId")
		if projectID == testProjectID {
			s.t.Errorf("unexpected delete of the configured project %q", projectID)
		}
		s.projectDeletes = append(s.projectDeletes, projectID)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+projectsPath, func(w http.ResponseWriter, r *http.Request) {
		s.t.Errorf("unexpected project create")
		w.WriteHeader(http.StatusNotImplemented)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func testMetaName(id cbdcuuid.UUID, purpose string, expiry time.Time) string {
	meta := stringclustermeta.MetaData{ID: id, Purpose: purpose, Expiry: expiry}
	return meta.String()
}

// The expiry in a name has a one second precision, so the test values stay
// far from now.
var (
	testExpired = time.Now().Add(-time.Hour)
	testLive    = time.Now().Add(time.Hour)
)

func TestCleanupRemovesExpiredSharedAndLegacyClusters(t *testing.T) {
	legacyID := cbdcuuid.New()

	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
			{ID: "p-legacy", Name: testMetaName(legacyID, "", testExpired)},
			{ID: "p-foreign", Name: "someone-else"},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-expired", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateHealthy},
				{ID: "c-live", Name: testMetaName(cbdcuuid.New(), "", testLive), State: capellav4.StateHealthy},
				{ID: "c-foreign", Name: "someone-else-cluster", State: capellav4.StateHealthy},
			},
			"p-legacy": {
				{ID: "c-legacy", Name: fmt.Sprintf("cbdc2_%s", legacyID), State: capellav4.StateHealthy},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"p-shared/c-expired", "p-legacy/c-legacy"}, srv.clusterDeletes)
	assert.Equal(t, []string{"p-legacy"}, srv.projectDeletes)
}

func TestCleanupRemovesLegacyWhenSharedListingFails(t *testing.T) {
	legacyID := cbdcuuid.New()

	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
			{ID: "p-legacy", Name: testMetaName(legacyID, "", testExpired)},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {},
			"p-legacy": {
				{ID: "c-legacy", Name: fmt.Sprintf("cbdc2_%s", legacyID), State: capellav4.StateHealthy},
			},
		},
		failListing: map[string]bool{"p-shared": true},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
	require.ErrorContains(t, err, "failed to list the clusters of the shared project")

	assert.Equal(t, []string{"p-legacy/c-legacy"}, srv.clusterDeletes)
	assert.Equal(t, []string{"p-legacy"}, srv.projectDeletes)
}

func TestCleanupKeepsDestroyFailedSharedCluster(t *testing.T) {
	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-expired", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateHealthy},
				{ID: "c-failed", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateDestroyFailed},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
	require.NoError(t, err)

	assert.Equal(t, []string{"p-shared/c-expired"}, srv.clusterDeletes)
	assert.Empty(t, srv.projectDeletes)
}

// A cleanup does not delete or wait on a shared cluster Capella already
// destroys, and logs why it skips it.
func TestCleanupKeepsDestroyingSharedCluster(t *testing.T) {
	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-expired", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateHealthy},
				{ID: "c-destroying", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateDestroying},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())
	core, logs := observer.New(zapcore.InfoLevel)
	deployer.logger = zap.New(core)

	err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
	require.NoError(t, err)

	assert.Equal(t, []string{"p-shared/c-expired"}, srv.clusterDeletes)
	assert.Empty(t, srv.projectDeletes)

	skipped := logs.FilterMessage("skipping expired cluster in destroying state").All()
	require.Len(t, skipped, 1)
	assert.Equal(t, "c-destroying", skipped[0].ContextMap()["cluster-id"])
	assert.Equal(t, "p-shared", skipped[0].ContextMap()["project-id"])

	for _, entry := range logs.FilterMessage("waiting for cluster removal to complete").All() {
		assert.NotEqual(t, "c-destroying", entry.ContextMap()["cluster-id"])
	}
}

func TestRemoveAllByPurposeRemovesMatchingSharedClusters(t *testing.T) {
	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
			// Out of scope, so its clusters are never listed.
			{ID: "p-legacy-bob", Name: testMetaName(cbdcuuid.New(), "bob", testExpired)},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-alice", Name: testMetaName(cbdcuuid.New(), "alice", testLive), State: capellav4.StateHealthy},
				{ID: "c-alice-x", Name: testMetaName(cbdcuuid.New(), "alice-x", testExpired), State: capellav4.StateHealthy},
				{ID: "c-alice-failed", Name: testMetaName(cbdcuuid.New(), "alice", testExpired), State: capellav4.StateDestroyFailed},
				{ID: "c-alice-destroying", Name: testMetaName(cbdcuuid.New(), "alice", testExpired), State: capellav4.StateDestroying},
				{ID: "c-alicebob", Name: testMetaName(cbdcuuid.New(), "alicebob", testExpired), State: capellav4.StateHealthy},
				{ID: "c-bob", Name: testMetaName(cbdcuuid.New(), "bob", testExpired), State: capellav4.StateHealthy},
				{ID: "c-foreign", Name: "alice", State: capellav4.StateHealthy},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.removeAll(context.Background(), deployment.RemoveAllOptions{Purpose: "alice"})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"p-shared/c-alice", "p-shared/c-alice-x", "p-shared/c-alice-failed",
		"p-shared/c-alice-destroying"}, srv.clusterDeletes)
	assert.Empty(t, srv.projectDeletes)
}

func TestRemoveAllReportsColumnarLookupAndRemovesTheRest(t *testing.T) {
	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-shared", Name: testMetaName(cbdcuuid.New(), "", testLive), State: capellav4.StateHealthy},
			},
		},
		columnars: map[string][]removalCluster{
			"p-shared": {
				{ID: "a-shared", Name: testMetaName(cbdcuuid.New(), "", testLive), State: capellav4.StateHealthy},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())

	// The test deployer has no v2 credentials, so the columnar lookup fails.
	err := deployer.removeAll(context.Background(), deployment.RemoveAllOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal capella v2 api")

	assert.Equal(t, []string{"p-shared/c-shared"}, srv.clusterDeletes)
	assert.Empty(t, srv.projectDeletes)
}

func TestCleanupDryRunDeletesNothing(t *testing.T) {
	legacyID := cbdcuuid.New()

	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
			{ID: "p-legacy", Name: testMetaName(legacyID, "", testExpired)},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-expired", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateHealthy},
				{ID: "c-failed", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateDestroyFailed},
				{ID: "c-destroying", Name: testMetaName(cbdcuuid.New(), "", testExpired), State: capellav4.StateDestroying},
				{ID: "c-live", Name: testMetaName(cbdcuuid.New(), "", testLive), State: capellav4.StateHealthy},
			},
			"p-legacy": {
				{ID: "c-legacy", Name: fmt.Sprintf("cbdc2_%s", legacyID), State: capellav4.StateHealthy},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())
	core, logs := observer.New(zapcore.InfoLevel)
	deployer.logger = zap.New(core)

	err := deployer.cleanup(context.Background(), deployment.CleanupOptions{DryRun: true})
	require.NoError(t, err)

	assert.Zero(t, srv.deletes)

	assert.Equal(t, 1, logs.FilterMessage("dry run, would remove the cluster").Len())
	keptLogs := logs.FilterMessage("dry run, would keep the cluster, it is skipped").All()
	require.Len(t, keptLogs, 2)
	reasons := map[string]any{}
	for _, entry := range keptLogs {
		reasons[entry.ContextMap()["cloud-cluster-id"].(string)] = entry.ContextMap()["reason"]
	}
	assert.Equal(t, map[string]any{
		"c-failed":     "skipping cluster in destroyFailed state, it needs manual removal",
		"c-destroying": "skipping expired cluster in destroying state",
	}, reasons)
	assert.Equal(t, 1, logs.FilterMessage("dry run, would remove the project and its clusters").Len())

	summary := logs.FilterMessage("dry run finished, nothing was removed").All()
	require.Len(t, summary, 1)
	fields := summary[0].ContextMap()
	assert.EqualValues(t, 1, fields["projects-would-remove"])
	assert.EqualValues(t, 3, fields["shared-clusters-expired"])
	assert.EqualValues(t, 1, fields["shared-clusters-would-remove"])
	assert.EqualValues(t, 2, fields["shared-clusters-would-keep"])
	assert.EqualValues(t, 4, fields["shared-clusters-total"])
}

func TestRemoveClusterInSharedProjectDeletesNoProject(t *testing.T) {
	sharedID := cbdcuuid.New()

	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
		},
		clusters: map[string][]removalCluster{
			"p-shared": {
				{ID: "c-other", Name: testMetaName(cbdcuuid.New(), "", testLive), State: capellav4.StateHealthy},
				{ID: "c-shared", Name: testMetaName(sharedID, "", testLive), State: capellav4.StateHealthy},
			},
		},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.RemoveCluster(context.Background(), sharedID.String())
	require.NoError(t, err)

	assert.Equal(t, []string{"p-shared/c-shared"}, srv.clusterDeletes)
	assert.Empty(t, srv.projectDeletes)
}

// The configured project can carry a name that parses as cbdc2 meta data. It
// still holds the clusters of other users, so it is never deleted.
func TestRemovalNeverDeletesConfiguredProjectWithCbdc2Name(t *testing.T) {
	newServer := func(t *testing.T) *removalServer {
		return &removalServer{
			t: t,
			projects: []capellav4.ProjectInfo{
				{ID: "p-shared", Name: testMetaName(cbdcuuid.New(), "alice", testExpired)},
			},
			clusters: map[string][]removalCluster{
				"p-shared": {
					{ID: "c-expired", Name: testMetaName(cbdcuuid.New(), "alice", testExpired), State: capellav4.StateHealthy},
					{ID: "c-foreign", Name: "someone-else-cluster", State: capellav4.StateHealthy},
				},
			},
		}
	}

	t.Run("cleanup", func(t *testing.T) {
		srv := newServer(t)
		deployer := newTestDeployer(t, srv.handler())

		err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
		require.NoError(t, err)

		assert.Equal(t, []string{"p-shared/c-expired"}, srv.clusterDeletes)
		assert.Empty(t, srv.projectDeletes)
	})

	t.Run("remove-all", func(t *testing.T) {
		srv := newServer(t)
		deployer := newTestDeployer(t, srv.handler())

		err := deployer.removeAll(context.Background(), deployment.RemoveAllOptions{})
		require.NoError(t, err)

		assert.Equal(t, []string{"p-shared/c-expired"}, srv.clusterDeletes)
		assert.Empty(t, srv.projectDeletes)
	})

	t.Run("delete guard", func(t *testing.T) {
		srv := newServer(t)
		deployer := newTestDeployer(t, srv.handler())

		err := deployer.deleteProject(context.Background(), "p-shared", srv.projects[0].Name)
		require.ErrorContains(t, err, "it is the configured capella project")
		assert.Zero(t, srv.deletes)
	})
}

func TestCleanupWithConfiguredProjectNotFound(t *testing.T) {
	legacyID := cbdcuuid.New()

	srv := &removalServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-legacy", Name: testMetaName(legacyID, "", testExpired)},
		},
		clusters: map[string][]removalCluster{
			"p-legacy": {
				{ID: "c-legacy", Name: fmt.Sprintf("cbdc2_%s", legacyID), State: capellav4.StateHealthy},
			},
		},
		missingProjects: map[string]bool{"p-shared": true},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
	require.NoError(t, err)

	assert.Equal(t, []string{"p-legacy/c-legacy"}, srv.clusterDeletes)
	assert.Equal(t, []string{"p-legacy"}, srv.projectDeletes)
}

// Without a project ID a removal handles only the legacy projects. The server
// fails the test on a cluster listing for any project not in clusters.
func TestRemovalWithoutProjectIDHandlesOnlyOldLayout(t *testing.T) {
	newServer := func(t *testing.T, legacyID cbdcuuid.UUID) *removalServer {
		return &removalServer{
			t: t,
			projects: []capellav4.ProjectInfo{
				{ID: "p-shared", Name: testSharedProjectName},
				{ID: "p-foreign", Name: "someone-else"},
				{ID: "p-legacy", Name: testMetaName(legacyID, "", testExpired)},
			},
			clusters: map[string][]removalCluster{
				"p-legacy": {
					{ID: "c-legacy", Name: fmt.Sprintf("cbdc2_%s", legacyID), State: capellav4.StateHealthy},
				},
			},
		}
	}

	t.Run("cleanup", func(t *testing.T) {
		srv := newServer(t, cbdcuuid.New())
		deployer := newTestDeployer(t, srv.handler())
		deployer.projectID = ""

		err := deployer.cleanup(context.Background(), deployment.CleanupOptions{})
		require.NoError(t, err)

		assert.Equal(t, []string{"p-legacy/c-legacy"}, srv.clusterDeletes)
		assert.Equal(t, []string{"p-legacy"}, srv.projectDeletes)
	})

	t.Run("remove-all", func(t *testing.T) {
		srv := newServer(t, cbdcuuid.New())
		deployer := newTestDeployer(t, srv.handler())
		deployer.projectID = ""

		err := deployer.removeAll(context.Background(), deployment.RemoveAllOptions{})
		require.NoError(t, err)

		assert.Equal(t, []string{"p-legacy/c-legacy"}, srv.clusterDeletes)
		assert.Equal(t, []string{"p-legacy"}, srv.projectDeletes)
	})
}
