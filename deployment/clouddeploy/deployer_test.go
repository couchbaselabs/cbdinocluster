package clouddeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/couchbaselabs/cbdinocluster/clusterdef"
	"github.com/couchbaselabs/cbdinocluster/utils/capellacontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
)

const testTenantID = "org-1"

// testProjectID is the configured project of the test deployer. The test
// deployer skips NewDeployer, so the ID needs no UUID form.
const testProjectID = "p-shared"

// testSharedProjectName is a name with no meaning to cbdinocluster.
const testSharedProjectName = "team-project"

func projectNameForID(t *testing.T, id cbdcuuid.UUID) string {
	t.Helper()
	meta := stringclustermeta.MetaData{
		ID:     id,
		Expiry: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	return meta.String()
}

func newTestDeployer(t *testing.T, handler http.Handler) *Deployer {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := capellav4.NewClient(&capellav4.ClientOptions{
		Endpoint:   srv.URL,
		SecretKeys: []string{"test-secret"},
	})
	require.NoError(t, err)

	return &Deployer{
		logger:    zap.NewNop(),
		v4:        client,
		v4mgr:     &capellav4.Manager{Logger: zap.NewNop(), Client: client},
		tenantID:  testTenantID,
		projectID: testProjectID,
	}
}

// createHandlerForFindClusters stubs the three v4 calls findClusters makes:
//
//	Client.ListProjects          GET /v4/organizations/{org}/projects
//	Client.ListClusters          GET /v4/organizations/{org}/projects/{project}/clusters
//	Client.ListAnalyticsClusters GET /v4/organizations/{org}/projects/{project}/analyticsClusters
//
// This can simulate the situation where projects included in the ListProjects response are deleted before
// the ListClusters call, so it can be used by tests to verify that findClusters can handle this gracefully.
func createHandlerForFindClusters(t *testing.T, projects map[string]string, deletedAfterListProjects map[string]bool) http.Handler {
	t.Helper()

	writeJson := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(body))
	}

	// ListProjects should respond with every cbdc2 project in the org.
	listProjects := func(w http.ResponseWriter, _ *http.Request) {
		var data []any
		for id, name := range projects {
			data = append(data, map[string]any{"id": id, "name": name})
		}
		writeJson(w, map[string]any{"data": data})
	}

	// Serves both ListClusters and ListAnalyticsClusters
	listUnderProject := func(w http.ResponseWriter, r *http.Request) {
		if deletedAfterListProjects[r.PathValue("projectId")] {
			w.WriteHeader(http.StatusNotFound)
			writeJson(w, map[string]any{
				"code":           2000,
				"httpStatusCode": 404,
				"message":        "The server cannot find a project by its ID.",
			})
			return
		}

		writeJson(w, map[string]any{})
	}

	projectsPath := "/v4/organizations/" + testTenantID + "/projects"

	mux := http.NewServeMux()
	// Client.ListProjects
	mux.HandleFunc("GET "+projectsPath, listProjects)
	// Client.ListClusters
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/clusters", listUnderProject)
	// Client.ListAnalyticsClusters
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/analyticsClusters", listUnderProject)

	// Any other endpoint shouldn't have been called by findClusters, fail with 'Unimplemented'
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request path %q", r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func TestFindClustersSkipsProjectDeletedWhileListing(t *testing.T) {
	liveID := cbdcuuid.New()
	goneID := cbdcuuid.New()

	deployer := newTestDeployer(t, createHandlerForFindClusters(t,
		map[string]string{
			"p-live": projectNameForID(t, liveID),
			"p-gone": projectNameForID(t, goneID),
		},
		map[string]bool{"p-gone": true},
	))

	clusters, err := deployer.findClusters(context.Background(), "")
	require.NoError(t, err)

	require.Len(t, clusters, 1)
	assert.Equal(t, "p-live", clusters[0].ProjectID)
	assert.Equal(t, liveID, clusters[0].Meta.ID)
}

func TestFindClustersSkipsSingleDeletedProject(t *testing.T) {
	goneID := cbdcuuid.New()

	deployer := newTestDeployer(t, createHandlerForFindClusters(t,
		map[string]string{"p-gone": projectNameForID(t, goneID)},
		map[string]bool{"p-gone": true},
	))

	clusters, err := deployer.findClusters(context.Background(), goneID.String())
	require.NoError(t, err)
	assert.Empty(t, clusters)
}

func TestFindClustersReportsOtherErrors(t *testing.T) {
	id := cbdcuuid.New()
	name := projectNameForID(t, id)

	deployer := newTestDeployer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/projects") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"id":"p-1","name":%q}]}`, name)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":1002,"httpStatusCode":403,"message":"access denied"}`))
	}))

	_, err := deployer.findClusters(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

func TestRemoveClusterAlreadyRemoved(t *testing.T) {
	t.Run("project not found", func(t *testing.T) {
		goneID := cbdcuuid.New()

		deployer := newTestDeployer(t, createHandlerForFindClusters(t,
			map[string]string{"p-gone": projectNameForID(t, goneID)},
			map[string]bool{"p-gone": true},
		))

		require.NoError(t, deployer.RemoveCluster(context.Background(), goneID.String()))
	})

	t.Run("cluster no longer listed", func(t *testing.T) {
		deployer := newTestDeployer(t, createHandlerForFindClusters(t,
			map[string]string{"p-other": projectNameForID(t, cbdcuuid.New())},
			nil,
		))

		require.NoError(t, deployer.RemoveCluster(context.Background(), cbdcuuid.New().String()))
	})

	t.Run("other errors still fail", func(t *testing.T) {
		id := cbdcuuid.New()
		name := projectNameForID(t, id)

		deployer := newTestDeployer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/projects") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"data":[{"id":"p-1","name":%q}]}`, name)
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":1002,"httpStatusCode":403,"message":"access denied"}`))
		}))

		err := deployer.RemoveCluster(context.Background(), id.String())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "access denied")
	})
}

func TestSplitProjects(t *testing.T) {
	legacyID := cbdcuuid.New()
	legacyName := projectNameForID(t, legacyID)

	t.Run("configured project found", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
			{ID: "p-legacy", Name: legacyName},
		}, "p-shared", zap.NewNop())

		require.NotNil(t, shared)
		assert.Equal(t, "p-shared", shared.ID)
		assert.Equal(t, testSharedProjectName, shared.Name)
		require.Len(t, legacy, 1)
		assert.Equal(t, "p-legacy", legacy[0].Info.ID)
		assert.Equal(t, legacyID, legacy[0].Meta.ID)
	})

	t.Run("configured project not in the list", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-legacy", Name: legacyName},
		}, "p-shared", zap.NewNop())

		require.NotNil(t, shared)
		assert.Equal(t, "p-shared", shared.ID)
		assert.Empty(t, shared.Name)
		require.Len(t, legacy, 1)
	})

	t.Run("no project id", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-other", Name: testSharedProjectName},
			{ID: "p-legacy", Name: legacyName},
		}, "", zap.NewNop())

		assert.Nil(t, shared)
		require.Len(t, legacy, 1)
		assert.Equal(t, "p-legacy", legacy[0].Info.ID)
	})

	t.Run("configured project with a cbdc2 name is not old layout", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-shared", Name: projectNameForID(t, cbdcuuid.New())},
			{ID: "p-legacy", Name: legacyName},
		}, "p-shared", zap.NewNop())

		require.NotNil(t, shared)
		assert.Equal(t, "p-shared", shared.ID)
		require.Len(t, legacy, 1)
		assert.Equal(t, "p-legacy", legacy[0].Info.ID)
	})

	t.Run("two projects with the same name", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-other", Name: testSharedProjectName},
			{ID: "p-shared", Name: testSharedProjectName},
		}, "p-shared", zap.NewNop())

		require.NotNil(t, shared)
		assert.Equal(t, "p-shared", shared.ID)
		assert.Empty(t, legacy)
	})

	t.Run("malformed cbdc2 name skipped", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-bad", Name: "cbdc2_notanid_20300102-030405"},
			{ID: "p-legacy", Name: legacyName},
		}, "", zap.NewNop())

		assert.Nil(t, shared)
		require.Len(t, legacy, 1)
		assert.Equal(t, "p-legacy", legacy[0].Info.ID)
	})

	t.Run("other projects ignored", func(t *testing.T) {
		shared, legacy := splitProjects([]*capellav4.ProjectInfo{
			{ID: "p-other", Name: "someone-else"},
			{ID: "p-prefix", Name: testSharedProjectName + "-2"},
		}, "", zap.NewNop())

		assert.Nil(t, shared)
		assert.Empty(t, legacy)
	})
}

func TestSharedClusterInfos(t *testing.T) {
	project := &capellav4.ProjectInfo{ID: "p-shared", Name: testSharedProjectName}
	clusterID := cbdcuuid.New()
	columnarID := cbdcuuid.New()
	oldLayoutID := cbdcuuid.New()

	infos := sharedClusterInfos(project,
		[]*capellav4.ClusterInfo{
			{ID: "c-meta", Name: projectNameForID(t, clusterID)},
			{ID: "c-foreign", Name: "someone-else"},
			{ID: "c-malformed", Name: "cbdc2_notanid_20300102-030405"},
			{ID: "c-old-layout", Name: fmt.Sprintf("cbdc2_%s", oldLayoutID)},
		},
		[]*capellav4.AnalyticsClusterInfo{
			{ID: "a-meta", Name: projectNameForID(t, columnarID)},
		},
		zap.NewNop())

	require.Len(t, infos, 2)

	assert.Equal(t, clusterID, infos[0].Meta.ID)
	assert.Equal(t, "p-shared", infos[0].ProjectID)
	assert.Equal(t, testSharedProjectName, infos[0].ProjectName)
	require.NotNil(t, infos[0].Cluster)
	assert.Equal(t, "c-meta", infos[0].Cluster.ID)
	assert.Nil(t, infos[0].Columnar)
	assert.False(t, infos[0].Legacy)
	assert.False(t, infos[0].IsCorrupted)

	assert.Equal(t, columnarID, infos[1].Meta.ID)
	assert.Nil(t, infos[1].Cluster)
	require.NotNil(t, infos[1].Columnar)
	assert.Equal(t, "a-meta", infos[1].Columnar.ID)
	assert.False(t, infos[1].Legacy)
}

// createHandlerForBothLayouts serves the project listing and the cluster
// listings of each project. A request for the clusters of a project with no
// entry in clusters fails the test.
func createHandlerForBothLayouts(
	t *testing.T,
	projects map[string]string,
	clusters map[string][]map[string]any,
) http.Handler {
	t.Helper()

	writeJson := func(w http.ResponseWriter, body any) {
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(body))
	}

	projectsPath := "/v4/organizations/" + testTenantID + "/projects"

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+projectsPath, func(w http.ResponseWriter, _ *http.Request) {
		var data []any
		for id, name := range projects {
			data = append(data, map[string]any{"id": id, "name": name})
		}
		writeJson(w, map[string]any{"data": data})
	})
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/clusters", func(w http.ResponseWriter, r *http.Request) {
		data, ok := clusters[r.PathValue("projectId")]
		if !ok {
			t.Errorf("unexpected cluster listing for project %q", r.PathValue("projectId"))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJson(w, map[string]any{"data": data})
	})
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/analyticsClusters", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := clusters[r.PathValue("projectId")]; !ok {
			t.Errorf("unexpected analytics cluster listing for project %q", r.PathValue("projectId"))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeJson(w, map[string]any{})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request path %q", r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func TestFindClustersReadsBothLayouts(t *testing.T) {
	legacyID := cbdcuuid.New()
	sharedID := cbdcuuid.New()

	deployer := newTestDeployer(t, createHandlerForBothLayouts(t,
		map[string]string{
			"p-legacy":  projectNameForID(t, legacyID),
			"p-shared":  testSharedProjectName,
			"p-foreign": "someone-else",
		},
		map[string][]map[string]any{
			"p-legacy": {{"id": "c-legacy", "name": fmt.Sprintf("cbdc2_%s", legacyID)}},
			"p-shared": {
				{"id": "c-shared", "name": projectNameForID(t, sharedID)},
				{"id": "c-foreign", "name": "someone-else-cluster"},
			},
		},
	))

	clusters, err := deployer.FindClusters(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, clusters, 2)

	byID := map[string]string{}
	for _, cluster := range clusters {
		byID[cluster.GetID()] = cluster.(*ClusterInfo).CloudProjectID
	}
	assert.Equal(t, map[string]string{
		legacyID.String(): "p-legacy",
		sharedID.String(): "p-shared",
	}, byID)
}

func TestFindClusterInfoFindsSharedCluster(t *testing.T) {
	legacyID := cbdcuuid.New()
	sharedID := cbdcuuid.New()
	otherSharedID := cbdcuuid.New()

	deployer := newTestDeployer(t, createHandlerForBothLayouts(t,
		map[string]string{
			"p-legacy": projectNameForID(t, legacyID),
			"p-shared": testSharedProjectName,
		},
		map[string][]map[string]any{
			"p-shared": {
				{"id": "c-other", "name": projectNameForID(t, otherSharedID)},
				{"id": "c-shared", "name": projectNameForID(t, sharedID)},
			},
		},
	))

	info, err := deployer.findClusterInfo(context.Background(), sharedID.String())
	require.NoError(t, err)

	assert.Equal(t, sharedID, info.Meta.ID)
	assert.Equal(t, "p-shared", info.ProjectID)
	require.NotNil(t, info.Cluster)
	assert.Equal(t, "c-shared", info.Cluster.ID)
	assert.False(t, info.Legacy)
}

// Without a project ID only the cbdc2 projects are read. The handler fails the
// test on a cluster listing for any other project.
func TestFindClustersWithoutProjectID(t *testing.T) {
	legacyID := cbdcuuid.New()

	deployer := newTestDeployer(t, createHandlerForBothLayouts(t,
		map[string]string{
			"p-legacy":  projectNameForID(t, legacyID),
			"p-shared":  testSharedProjectName,
			"p-foreign": "someone-else",
		},
		map[string][]map[string]any{
			"p-legacy": {{"id": "c-legacy", "name": fmt.Sprintf("cbdc2_%s", legacyID)}},
		},
	))
	deployer.projectID = ""

	clusters, err := deployer.findClusters(context.Background(), "")
	require.NoError(t, err)

	require.Len(t, clusters, 1)
	assert.Equal(t, legacyID, clusters[0].Meta.ID)
	assert.Equal(t, "p-legacy", clusters[0].ProjectID)
	assert.True(t, clusters[0].Legacy)

	info, err := deployer.findClusterInfo(context.Background(), legacyID.String())
	require.NoError(t, err)
	assert.True(t, info.Legacy)

	_, err = deployer.findClusterInfo(context.Background(), cbdcuuid.New().String())
	require.ErrorContains(t, err, "failed to find cluster")
}

// A configured project that is gone must not block the old layout.
func TestFindClustersConfiguredProjectNotFound(t *testing.T) {
	legacyID := cbdcuuid.New()

	deployer := newTestDeployer(t, createHandlerForFindClusters(t,
		map[string]string{"p-legacy": projectNameForID(t, legacyID)},
		map[string]bool{"p-shared": true},
	))
	core, logs := observer.New(zapcore.WarnLevel)
	deployer.logger = zap.New(core)

	clusters, err := deployer.findClusters(context.Background(), "")
	require.NoError(t, err)

	require.Len(t, clusters, 1)
	assert.Equal(t, "p-legacy", clusters[0].ProjectID)

	warnings := logs.FilterMessage("the configured capella project does not exist, skipping its clusters").All()
	require.NotEmpty(t, warnings)
	assert.Equal(t, "p-shared", warnings[0].ContextMap()["project-id"])
}

type clusterCreateCall struct {
	ProjectID string
	FreeTier  bool
	Name      string
}

// allocateServer stubs the v4 calls of an allocate and records the writes. Any
// project listing, create or delete fails the test, because an allocate uses
// the configured project as it is.
type allocateServer struct {
	t *testing.T

	mu sync.Mutex

	// A non zero status fails the call with that status.
	createClusterStatus int
	// projectNotFound makes the cluster create answer project not found.
	projectNotFound bool

	clusterCreates []clusterCreateCall
	projectDeletes int
}

func (s *allocateServer) handler() http.Handler {
	writeJson := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		assert.NoError(s.t, json.NewEncoder(w).Encode(body))
	}
	writeError := func(w http.ResponseWriter, status int, code int) {
		writeJson(w, status, map[string]any{
			"code":           code,
			"httpStatusCode": status,
			"message":        "stub failure",
		})
	}
	createCluster := func(freeTier bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name string `json:"name"`
			}
			assert.NoError(s.t, json.NewDecoder(r.Body).Decode(&body))

			s.mu.Lock()
			defer s.mu.Unlock()
			s.clusterCreates = append(s.clusterCreates, clusterCreateCall{
				ProjectID: r.PathValue("projectId"),
				FreeTier:  freeTier,
				Name:      body.Name,
			})
			if s.projectNotFound {
				writeError(w, http.StatusNotFound, 2000)
				return
			}
			if s.createClusterStatus != 0 {
				writeError(w, s.createClusterStatus, 1000)
				return
			}
			writeJson(w, http.StatusAccepted, map[string]any{"id": "c-new"})
		}
	}

	projectsPath := "/v4/organizations/" + testTenantID + "/projects"

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+projectsPath, func(w http.ResponseWriter, r *http.Request) {
		s.t.Errorf("unexpected project create")
		w.WriteHeader(http.StatusNotImplemented)
	})
	mux.HandleFunc("DELETE "+projectsPath+"/{projectId}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.projectDeletes++
		s.mu.Unlock()
		s.t.Errorf("unexpected project delete for %q", r.PathValue("projectId"))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+projectsPath+"/{projectId}/clusters", createCluster(false))
	mux.HandleFunc("POST "+projectsPath+"/{projectId}/clusters/freeTier", createCluster(true))
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/clusters/{clusterId}", func(w http.ResponseWriter, r *http.Request) {
		writeJson(w, http.StatusOK, map[string]any{
			"id":           r.PathValue("clusterId"),
			"currentState": capellav4.StateHealthy,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func testClusterDef(purpose string) *clusterdef.Cluster {
	return &clusterdef.Cluster{
		Expiry:  time.Hour,
		Purpose: purpose,
		Cloud: clusterdef.CloudCluster{
			CloudProvider: capellav4.ProviderAws,
			Region:        "us-east-1",
		},
		NodeGroups: []*clusterdef.NodeGroup{{Count: 3}},
	}
}

func TestCreateNewClusterUsesSharedProject(t *testing.T) {
	srv := &allocateServer{t: t}
	deployer := newTestDeployer(t, srv.handler())

	info, err := deployer.createNewCluster(context.Background(), testClusterDef("sdk-nightly"), "")
	require.NoError(t, err)

	assert.Zero(t, srv.projectDeletes)
	require.Len(t, srv.clusterCreates, 1)
	assert.Equal(t, "p-shared", srv.clusterCreates[0].ProjectID)
	assert.False(t, srv.clusterCreates[0].FreeTier)

	parsed := parseMetaName(t, srv.clusterCreates[0].Name)
	assert.Equal(t, info.GetID(), parsed.ID.String())
	assert.Equal(t, "sdk-nightly", parsed.Purpose)
	assert.Equal(t, "sdk-nightly", info.GetPurpose())
	assert.Equal(t, "p-shared", info.(*ClusterInfo).CloudProjectID)
	assert.Equal(t, "c-new", info.(*ClusterInfo).CloudClusterID)
}

const errNoProjectIDText = `a capella project id is required, run "cbdinocluster init" to find or create the CBDC2_SHARED project, or run "cbdinocluster cloud projects create <name>" and set the id with "cbdinocluster init --capella-project-id <id>"`

func TestAllocateWithoutProjectIDFails(t *testing.T) {
	failOnRequest := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	freeTier := testClusterDef("free")
	freeTier.Cloud.FreeTier = true
	freeTier.NodeGroups = nil

	for name, def := range map[string]*clusterdef.Cluster{
		"cluster":   testClusterDef("sdk"),
		"free tier": freeTier,
	} {
		t.Run(name, func(t *testing.T) {
			deployer := newTestDeployer(t, failOnRequest)
			deployer.projectID = ""

			_, err := deployer.createNewCluster(context.Background(), def, "")
			require.EqualError(t, err, errNoProjectIDText)
		})
	}

	t.Run("server image", func(t *testing.T) {
		// The v2 client stays nil, so any v2 call would panic.
		deployer := newTestDeployer(t, failOnRequest)
		deployer.projectID = ""
		deployer.hasLegacyCredentials = true

		_, err := deployer.deployNewCluster(context.Background(), testClusterDef("sdk"), "7.6.0", "image")
		require.EqualError(t, err, errNoProjectIDText)
	})
}

func TestCreateNewClusterInMissingProject(t *testing.T) {
	freeTier := testClusterDef("free")
	freeTier.Cloud.FreeTier = true
	freeTier.NodeGroups = nil

	for name, def := range map[string]*clusterdef.Cluster{
		"cluster":   testClusterDef("sdk"),
		"free tier": freeTier,
	} {
		t.Run(name, func(t *testing.T) {
			srv := &allocateServer{t: t, projectNotFound: true}
			deployer := newTestDeployer(t, srv.handler())

			_, err := deployer.createNewCluster(context.Background(), def, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "the configured capella project p-shared does not exist")
			assert.True(t, capellav4.IsProjectNotFound(err))
			assert.Len(t, srv.clusterCreates, 1)
			assert.Zero(t, srv.projectDeletes)
		})
	}
}

func TestCreateNewClusterReturnsTrimmedPurpose(t *testing.T) {
	srv := &allocateServer{t: t}
	deployer := newTestDeployer(t, srv.handler())

	info, err := deployer.createNewCluster(context.Background(), testClusterDef(strings.Repeat("p", 300)), "")
	require.NoError(t, err)

	require.Len(t, srv.clusterCreates, 1)
	name := srv.clusterCreates[0].Name
	assert.Len(t, name, maxClusterNameLen)
	parsed := parseMetaName(t, name)
	assert.Less(t, len(parsed.Purpose), 300)
	assert.Equal(t, parsed.Purpose, info.GetPurpose())
}

func TestCreateNewClusterFailureDeletesNoProject(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusUnprocessableEntity} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := &allocateServer{
				t:                   t,
				createClusterStatus: status,
			}
			deployer := newTestDeployer(t, srv.handler())

			_, err := deployer.createNewCluster(context.Background(), testClusterDef("sdk"), "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to create cluster")

			assert.Len(t, srv.clusterCreates, 1)
			assert.Zero(t, srv.projectDeletes)
		})
	}
}

func TestCreateNewFreeTierClusterUsesSharedProject(t *testing.T) {
	srv := &allocateServer{t: t}
	deployer := newTestDeployer(t, srv.handler())

	def := testClusterDef("free")
	def.Cloud.FreeTier = true
	def.NodeGroups = nil

	info, err := deployer.createNewCluster(context.Background(), def, "")
	require.NoError(t, err)

	require.Len(t, srv.clusterCreates, 1)
	assert.Equal(t, "p-shared", srv.clusterCreates[0].ProjectID)
	assert.True(t, srv.clusterCreates[0].FreeTier)

	parsed := parseMetaName(t, srv.clusterCreates[0].Name)
	assert.Equal(t, info.GetID(), parsed.ID.String())
	assert.Equal(t, "free", parsed.Purpose)
}

type expiryWrite struct {
	Method string
	Path   string
	Body   map[string]any
}

// expiryServer stubs the v4 calls of an expiry change and records the writes.
// A cluster listing for a project with no entry in clusters fails the test.
type expiryServer struct {
	t *testing.T

	mu        sync.Mutex
	projects  []capellav4.ProjectInfo
	clusters  map[string][]map[string]any
	columnars map[string][]map[string]any

	// A non zero status fails the call with that status.
	updateClusterStatus  int
	updateFreeTierStatus int

	writes []expiryWrite
}

func (s *expiryServer) handler() http.Handler {
	writeJson := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		assert.NoError(s.t, json.NewEncoder(w).Encode(body))
	}
	listing := func(source func() map[string][]map[string]any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			projectID := r.PathValue("projectId")
			if _, ok := s.clusters[projectID]; !ok {
				s.t.Errorf("unexpected listing %q for project %q", r.URL.Path, projectID)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			writeJson(w, http.StatusOK, map[string]any{"data": source()[projectID]})
		}
	}
	update := func(status func() int, message string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			defer s.mu.Unlock()
			var body map[string]any
			assert.NoError(s.t, json.NewDecoder(r.Body).Decode(&body))
			s.writes = append(s.writes, expiryWrite{Method: r.Method, Path: r.URL.Path, Body: body})
			if code := status(); code != 0 {
				writeJson(w, code, map[string]any{
					"code":           4000,
					"httpStatusCode": code,
					"message":        message,
				})
				return
			}
			w.WriteHeader(http.StatusNoContent)
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
		listing(func() map[string][]map[string]any { return s.clusters }))
	mux.HandleFunc("GET "+projectsPath+"/{projectId}/analyticsClusters",
		listing(func() map[string][]map[string]any { return s.columnars }))
	mux.HandleFunc("PUT "+projectsPath+"/{projectId}",
		update(func() int { return 0 }, ""))
	mux.HandleFunc("PUT "+projectsPath+"/{projectId}/clusters/{clusterId}",
		update(func() int { return s.updateClusterStatus }, "generic update rejected"))
	mux.HandleFunc("PUT "+projectsPath+"/{projectId}/clusters/freeTier/{clusterId}",
		update(func() int { return s.updateFreeTierStatus }, "free tier update rejected"))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func (s *expiryServer) writePaths() []string {
	var out []string
	for _, write := range s.writes {
		out = append(out, write.Method+" "+write.Path)
	}
	return out
}

var (
	testExpiryOld = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	// Not UTC and not on a whole second, so the test covers both conversions.
	testExpiryNew = time.Date(2031, 6, 7, 9, 10, 11, 500_000_000, time.FixedZone("UTC+2", 2*60*60))
)

func testServiceGroups() []any {
	return []any{map[string]any{
		"node": map[string]any{
			"compute": map[string]any{"cpu": 4, "ram": 16},
			"disk":    map[string]any{"type": "gp3", "storage": 50, "iops": 3000},
		},
		"numOfNodes": 3,
		"services":   []any{"data", "index"},
	}}
}

func newSharedExpiryServer(t *testing.T, id cbdcuuid.UUID) *expiryServer {
	return &expiryServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
		},
		clusters: map[string][]map[string]any{
			"p-shared": {
				{"id": "c-other", "name": testMetaName(cbdcuuid.New(), "bob", testExpiryOld)},
				{
					"id":            "c-shared",
					"name":          testMetaName(id, "alice", testExpiryOld),
					"description":   "my cluster",
					"support":       map[string]any{"plan": "developer pro", "timezone": "PT"},
					"serviceGroups": testServiceGroups(),
				},
			},
		},
	}
}

func assertNewExpiryName(t *testing.T, id cbdcuuid.UUID, name any) {
	t.Helper()
	nameStr, ok := name.(string)
	require.True(t, ok)
	parsed := parseMetaName(t, nameStr)
	assert.Equal(t, id, parsed.ID)
	assert.Equal(t, "alice", parsed.Purpose)
	assert.True(t, parsed.Expiry.Equal(testExpiryNew.Truncate(time.Second)),
		"expiry %s, expected %s", parsed.Expiry, testExpiryNew.UTC())
}

func TestUpdateClusterExpiryRenamesSharedCluster(t *testing.T) {
	id := cbdcuuid.New()
	srv := newSharedExpiryServer(t, id)
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.UpdateClusterExpiry(context.Background(), id.String(), testExpiryNew)
	require.NoError(t, err)

	require.Equal(t, []string{
		"PUT /v4/organizations/" + testTenantID + "/projects/p-shared/clusters/c-shared",
	}, srv.writePaths())

	body := srv.writes[0].Body
	assertNewExpiryName(t, id, body["name"])
	assert.Equal(t, "my cluster", body["description"])
	assert.Equal(t, map[string]any{"plan": "developer pro", "timezone": "PT"}, body["support"])

	// Compare through JSON so the numbers have the same type on both sides.
	wantGroups, err := json.Marshal(testServiceGroups())
	require.NoError(t, err)
	gotGroups, err := json.Marshal(body["serviceGroups"])
	require.NoError(t, err)
	assert.JSONEq(t, string(wantGroups), string(gotGroups))
}

func TestUpdateClusterExpiryFallsBackToFreeTier(t *testing.T) {
	id := cbdcuuid.New()
	srv := newSharedExpiryServer(t, id)
	srv.updateClusterStatus = http.StatusUnprocessableEntity
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.UpdateClusterExpiry(context.Background(), id.String(), testExpiryNew)
	require.NoError(t, err)

	require.Equal(t, []string{
		"PUT /v4/organizations/" + testTenantID + "/projects/p-shared/clusters/c-shared",
		"PUT /v4/organizations/" + testTenantID + "/projects/p-shared/clusters/freeTier/c-shared",
	}, srv.writePaths())

	body := srv.writes[1].Body
	assert.Len(t, body, 2)
	assertNewExpiryName(t, id, body["name"])
	assert.Equal(t, "my cluster", body["description"])
}

func TestUpdateClusterExpiryReportsBothErrors(t *testing.T) {
	id := cbdcuuid.New()
	srv := newSharedExpiryServer(t, id)
	srv.updateClusterStatus = http.StatusUnprocessableEntity
	srv.updateFreeTierStatus = http.StatusUnprocessableEntity
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.UpdateClusterExpiry(context.Background(), id.String(), testExpiryNew)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generic update rejected")
	assert.Contains(t, err.Error(), "free tier update rejected")
	assert.Len(t, srv.writes, 2)
}

func TestUpdateClusterExpiryRenamesLegacyProject(t *testing.T) {
	id := cbdcuuid.New()
	srv := &expiryServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-legacy", Name: testMetaName(id, "alice", testExpiryOld)},
		},
		clusters: map[string][]map[string]any{
			"p-legacy": {{"id": "c-legacy", "name": fmt.Sprintf("cbdc2_%s", id)}},
		},
	}
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.UpdateClusterExpiry(context.Background(), id.String(), testExpiryNew)
	require.NoError(t, err)

	require.Equal(t, []string{
		"PUT /v4/organizations/" + testTenantID + "/projects/p-legacy",
	}, srv.writePaths())
	assertNewExpiryName(t, id, srv.writes[0].Body["name"])
}

func newSharedColumnarExpiryServer(t *testing.T, id cbdcuuid.UUID) *expiryServer {
	return &expiryServer{
		t: t,
		projects: []capellav4.ProjectInfo{
			{ID: "p-shared", Name: testSharedProjectName},
		},
		clusters: map[string][]map[string]any{"p-shared": {}},
		columnars: map[string][]map[string]any{
			"p-shared": {{
				"id":          "a-shared",
				"name":        testMetaName(id, "alice", testExpiryOld),
				"description": "my columnar",
				"nodes":       4,
			}},
		},
	}
}

func TestUpdateClusterExpiryRenamesSharedColumnar(t *testing.T) {
	id := cbdcuuid.New()
	srv := newSharedColumnarExpiryServer(t, id)
	deployer := newTestDeployer(t, srv.handler())

	var v2Writes []expiryWrite
	v2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/sessions" {
			_, _ = w.Write([]byte(`{"jwt":"test-jwt"}`))
			return
		}
		var body map[string]any
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		v2Writes = append(v2Writes, expiryWrite{Method: r.Method, Path: r.URL.Path, Body: body})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(v2.Close)

	controller, err := capellacontrol.NewController(context.Background(), &capellacontrol.ControllerOptions{
		Logger:   zap.NewNop(),
		Endpoint: v2.URL,
		Auth:     &capellacontrol.BasicCredentials{Username: "test-user", Password: "test-password"},
	})
	require.NoError(t, err)
	deployer.client = controller
	deployer.hasLegacyCredentials = true

	err = deployer.UpdateClusterExpiry(context.Background(), id.String(), testExpiryNew)
	require.NoError(t, err)

	assert.Empty(t, srv.writes)
	require.Len(t, v2Writes, 1)
	assert.Equal(t, http.MethodPatch, v2Writes[0].Method)
	assert.Equal(t, "/v2/organizations/"+testTenantID+"/projects/p-shared/instance/a-shared", v2Writes[0].Path)
	assertNewExpiryName(t, id, v2Writes[0].Body["name"])
	assert.Equal(t, "my columnar", v2Writes[0].Body["description"])
	assert.EqualValues(t, 4, v2Writes[0].Body["nodes"])
}

func TestUpdateClusterExpiryOfColumnarNeedsV2Credentials(t *testing.T) {
	id := cbdcuuid.New()
	srv := newSharedColumnarExpiryServer(t, id)
	deployer := newTestDeployer(t, srv.handler())

	err := deployer.UpdateClusterExpiry(context.Background(), id.String(), testExpiryNew)
	require.ErrorContains(t, err, "internal capella v2 api")
	assert.Empty(t, srv.writes)
}
