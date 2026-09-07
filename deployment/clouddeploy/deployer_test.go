package clouddeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/cbdcuuid"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
)

const testTenantID = "org-1"

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
		logger:   zap.NewNop(),
		v4:       client,
		v4mgr:    &capellav4.Manager{Logger: zap.NewNop(), Client: client},
		tenantID: testTenantID,
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
