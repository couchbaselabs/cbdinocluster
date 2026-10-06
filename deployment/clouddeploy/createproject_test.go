package clouddeploy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/couchbaselabs/cbdinocluster/utils/stringclustermeta"
)

func TestCheckNewProjectName(t *testing.T) {
	valid := []string{
		"team-project",
		"my-cbdc2_project",
		"Cbdc2_project",
		strings.Repeat("a", maxProjectNameLen),
		strings.Repeat("é", maxProjectNameLen),
	}
	for _, name := range valid {
		assert.NoError(t, CheckNewProjectName(name), "name %q", name)
	}

	invalid := []string{
		"",
		strings.Repeat("a", maxProjectNameLen+1),
		strings.Repeat("é", maxProjectNameLen+1),
		"cbdc2_",
		"cbdc2_shared",
		"cbdc2_0123456789abcdef_20261005-120000",
	}
	for _, name := range invalid {
		assert.Error(t, CheckNewProjectName(name), "name %q", name)
	}
}

// createProjectServer stubs the project list and create calls and records
// every create. Any other call fails the test.
type createProjectServer struct {
	t *testing.T

	mu sync.Mutex

	projects []capellav4.ProjectInfo
	// A non zero status fails the list or the create with that status.
	listStatus   int
	createStatus int

	creates []capellav4.CreateProjectRequest
}

func (s *createProjectServer) handler() http.Handler {
	writeJson := func(w http.ResponseWriter, status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		assert.NoError(s.t, json.NewEncoder(w).Encode(body))
	}
	writeError := func(w http.ResponseWriter, status int) {
		writeJson(w, status, map[string]any{
			"code":           1000,
			"httpStatusCode": status,
			"message":        "stub failure",
		})
	}

	projectsPath := "/v4/organizations/" + testTenantID + "/projects"

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+projectsPath, func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.listStatus != 0 {
			writeError(w, s.listStatus)
			return
		}
		var data []any
		for _, project := range s.projects {
			data = append(data, map[string]any{
				"id":    project.ID,
				"name":  project.Name,
				"audit": map[string]any{"createdAt": project.Audit.CreatedAt},
			})
		}
		writeJson(w, http.StatusOK, map[string]any{"data": data})
	})
	mux.HandleFunc("POST "+projectsPath, func(w http.ResponseWriter, r *http.Request) {
		var req capellav4.CreateProjectRequest
		assert.NoError(s.t, json.NewDecoder(r.Body).Decode(&req))

		s.mu.Lock()
		defer s.mu.Unlock()
		s.creates = append(s.creates, req)
		if s.createStatus != 0 {
			writeError(w, s.createStatus)
			return
		}
		writeJson(w, http.StatusCreated, map[string]any{"id": "p-created"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})

	return mux
}

func TestCreateProjectNewName(t *testing.T) {
	srv := &createProjectServer{t: t, projects: []capellav4.ProjectInfo{
		{ID: "p-other", Name: "someone-else"},
	}}
	deployer := newTestDeployer(t, srv.handler())
	deployer.projectID = ""

	projectID, err := deployer.CreateProject(context.Background(), "team-project", "Clusters of the team")
	require.NoError(t, err)
	assert.Equal(t, "p-created", projectID)
	require.Len(t, srv.creates, 1)
	assert.Equal(t, "team-project", srv.creates[0].Name)
	assert.Equal(t, "Clusters of the team", srv.creates[0].Description)
}

func TestCreateProjectExistingName(t *testing.T) {
	srv := &createProjectServer{t: t, projects: []capellav4.ProjectInfo{
		{ID: "p-other", Name: "someone-else"},
		{ID: "p-existing", Name: "team-project"},
	}}
	deployer := newTestDeployer(t, srv.handler())

	_, err := deployer.CreateProject(context.Background(), "team-project", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "p-existing")
	assert.NotContains(t, err.Error(), "p-other")
	assert.Empty(t, srv.creates)
}

func TestCreateProjectNameUsedTwice(t *testing.T) {
	srv := &createProjectServer{t: t, projects: []capellav4.ProjectInfo{
		{ID: "p-existing-1", Name: "team-project"},
		{ID: "p-existing-2", Name: "team-project"},
	}}
	deployer := newTestDeployer(t, srv.handler())

	_, err := deployer.CreateProject(context.Background(), "team-project", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "p-existing-1")
	assert.Contains(t, err.Error(), "p-existing-2")
	assert.Empty(t, srv.creates)
}

func TestCreateProjectCreateFails(t *testing.T) {
	srv := &createProjectServer{t: t, createStatus: http.StatusForbidden}
	deployer := newTestDeployer(t, srv.handler())

	_, err := deployer.CreateProject(context.Background(), "team-project", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `failed to create the capella project "team-project"`)
	assert.Contains(t, err.Error(), "stub failure")
	assert.Len(t, srv.creates, 1)
}

func TestCreateProjectBadNameMakesNoCall(t *testing.T) {
	srv := &createProjectServer{t: t}
	deployer := newTestDeployer(t, srv.handler())

	_, err := deployer.CreateProject(context.Background(), "cbdc2_shared", "")
	require.ErrorContains(t, err, "starts with cbdc2_")
	assert.Empty(t, srv.creates)
}

// The shared project must never look like an old layout project, because
// cbdinocluster deletes those.
func TestSharedProjectName(t *testing.T) {
	assert.NoError(t, CheckNewProjectName(SharedProjectName))

	meta, err := stringclustermeta.Parse(SharedProjectName)
	assert.NoError(t, err)
	assert.Nil(t, meta)

	assert.False(t, canDeleteProjectName(SharedProjectName))
}

func projectCreatedAt(id, createdAt string) *capellav4.ProjectInfo {
	return &capellav4.ProjectInfo{
		ID:    id,
		Name:  SharedProjectName,
		Audit: capellav4.Audit{CreatedAt: createdAt},
	}
}

func TestOldestProject(t *testing.T) {
	t.Run("one", func(t *testing.T) {
		project := projectCreatedAt("p-1", "2026-01-02T03:04:05Z")
		assert.Same(t, project, oldestProject([]*capellav4.ProjectInfo{project}))
	})

	t.Run("several out of order", func(t *testing.T) {
		oldest := oldestProject([]*capellav4.ProjectInfo{
			projectCreatedAt("p-new", "2026-03-01T00:00:00Z"),
			projectCreatedAt("p-old", "2025-12-31T23:59:59.123456789Z"),
			projectCreatedAt("p-mid", "2026-01-15T10:00:00+02:00"),
		})
		require.NotNil(t, oldest)
		assert.Equal(t, "p-old", oldest.ID)
	})

	t.Run("unparsable time sorts last", func(t *testing.T) {
		oldest := oldestProject([]*capellav4.ProjectInfo{
			projectCreatedAt("p-bad", "yesterday"),
			projectCreatedAt("p-empty", ""),
			projectCreatedAt("p-dated", "2026-03-01T00:00:00Z"),
		})
		require.NotNil(t, oldest)
		assert.Equal(t, "p-dated", oldest.ID)
	})

	t.Run("none", func(t *testing.T) {
		assert.Nil(t, oldestProject(nil))
	})
}

func TestFindSharedProject(t *testing.T) {
	t.Run("found once", func(t *testing.T) {
		srv := &createProjectServer{t: t, projects: []capellav4.ProjectInfo{
			{ID: "p-other", Name: "someone-else"},
			*projectCreatedAt("p-shared", "2026-01-02T03:04:05Z"),
		}}
		deployer := newTestDeployer(t, srv.handler())

		project, count, err := FindSharedProject(context.Background(), deployer.v4, deployer.tenantID)
		require.NoError(t, err)
		require.NotNil(t, project)
		assert.Equal(t, "p-shared", project.ID)
		assert.Equal(t, 1, count)
		assert.Empty(t, srv.creates)
	})

	t.Run("found several", func(t *testing.T) {
		srv := &createProjectServer{t: t, projects: []capellav4.ProjectInfo{
			*projectCreatedAt("p-new", "2026-03-01T00:00:00Z"),
			{ID: "p-lower", Name: "cbdc2_shared"},
			*projectCreatedAt("p-old", "2025-06-01T00:00:00Z"),
		}}
		deployer := newTestDeployer(t, srv.handler())

		project, count, err := FindSharedProject(context.Background(), deployer.v4, deployer.tenantID)
		require.NoError(t, err)
		require.NotNil(t, project)
		assert.Equal(t, "p-old", project.ID)
		assert.Equal(t, 2, count)
	})

	t.Run("none", func(t *testing.T) {
		srv := &createProjectServer{t: t, projects: []capellav4.ProjectInfo{
			{ID: "p-other", Name: "someone-else"},
		}}
		deployer := newTestDeployer(t, srv.handler())

		project, count, err := FindSharedProject(context.Background(), deployer.v4, deployer.tenantID)
		require.NoError(t, err)
		assert.Nil(t, project)
		assert.Zero(t, count)
		assert.Empty(t, srv.creates)
	})

	t.Run("list error", func(t *testing.T) {
		srv := &createProjectServer{t: t, listStatus: http.StatusForbidden}
		deployer := newTestDeployer(t, srv.handler())

		_, _, err := FindSharedProject(context.Background(), deployer.v4, deployer.tenantID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list projects")
		assert.Contains(t, err.Error(), "stub failure")
	})
}
