package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	testSharedProjectOldID = "11111111-1111-1111-1111-111111111111"
	testSharedProjectNewID = "22222222-2222-2222-2222-222222222222"
	testCreatedProjectID   = "33333333-3333-3333-3333-333333333333"
	testTypedProjectID     = "44444444-4444-4444-4444-444444444444"
)

// fakeCapellaProjects serves the v4 project list and create calls.
type fakeCapellaProjects struct {
	mu sync.Mutex

	projects   []map[string]any
	listStatus int

	creates []capellav4.CreateProjectRequest
}

func (f *fakeCapellaProjects) client(t *testing.T) *capellav4.Client {
	t.Helper()

	const projectsPath = "/v4/organizations/org-1/projects"

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+projectsPath, func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f.listStatus != 0 {
			w.WriteHeader(f.listStatus)
			_, _ = w.Write([]byte(`{"code":1000,"httpStatusCode":403,"message":"stub failure"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": f.projects})
	})
	mux.HandleFunc("POST "+projectsPath, func(w http.ResponseWriter, r *http.Request) {
		var req capellav4.CreateProjectRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		f.mu.Lock()
		f.creates = append(f.creates, req)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": testCreatedProjectID})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %q", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client, err := capellav4.NewClient(&capellav4.ClientOptions{
		Logger:     zap.NewNop(),
		Endpoint:   srv.URL,
		SecretKeys: []string{"test-secret"},
	})
	require.NoError(t, err)
	return client
}

func (f *fakeCapellaProjects) createRequests() []capellav4.CreateProjectRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capellav4.CreateProjectRequest(nil), f.creates...)
}

// scriptedAnswers stands in for the init prompts. Every prompt takes the next
// answer, and a prompt with no answer left fails the test.
type scriptedAnswers struct {
	t         *testing.T
	answers   []string
	questions []string
}

func (s *scriptedAnswers) next(q string) string {
	s.questions = append(s.questions, q)
	if len(s.answers) == 0 {
		s.t.Fatalf("no scripted answer for %q", q)
	}
	answer := s.answers[0]
	s.answers = s.answers[1:]
	return answer
}

func (s *scriptedAnswers) readBool(q string, defaultValue bool) bool {
	switch answer := s.next(q); answer {
	case "":
		return defaultValue
	case "y":
		return true
	case "n":
		return false
	default:
		s.t.Fatalf("bad scripted bool answer %q", answer)
		return false
	}
}

func (s *scriptedAnswers) readString(q string, defaultValue string, _ bool) string {
	if answer := s.next(q); answer != "" {
		return answer
	}
	return defaultValue
}

func runChooseCapellaSharedProject(
	t *testing.T,
	fake *fakeCapellaProjects,
	answers ...string,
) (string, string, *scriptedAnswers) {
	t.Helper()

	script := &scriptedAnswers{t: t, answers: answers}
	client := fake.client(t)

	var projectID string
	stdout, _ := captureOutput(t, func() {
		projectID = chooseCapellaSharedProject(context.Background(), zap.NewNop(), client,
			"org-1", script.readBool, script.readString)
	})
	assert.Empty(t, script.answers, "unused scripted answers")
	return projectID, stdout, script
}

func sharedProjectEntry(id, createdAt string) map[string]any {
	return map[string]any{
		"id":    id,
		"name":  "CBDC2_SHARED",
		"audit": map[string]any{"createdAt": createdAt},
	}
}

func TestChooseCapellaSharedProjectFound(t *testing.T) {
	fake := &fakeCapellaProjects{projects: []map[string]any{
		sharedProjectEntry(testSharedProjectNewID, "2026-03-01T00:00:00Z"),
		{"id": "p-other", "name": "someone-else"},
		sharedProjectEntry(testSharedProjectOldID, "2025-06-01T00:00:00Z"),
	}}

	projectID, stdout, script := runChooseCapellaSharedProject(t, fake, "")

	assert.Equal(t, testSharedProjectOldID, projectID)
	assert.Contains(t, stdout,
		"Found the shared Capella project CBDC2_SHARED ("+testSharedProjectOldID+").\n")
	assert.Contains(t, stdout, "2 projects carry this name, using the oldest.\n")
	assert.Equal(t, []string{"Use it for new clusters?"}, script.questions)
	assert.Empty(t, fake.createRequests())
}

func TestChooseCapellaSharedProjectFoundButDeclined(t *testing.T) {
	fake := &fakeCapellaProjects{projects: []map[string]any{
		sharedProjectEntry(testSharedProjectOldID, "2025-06-01T00:00:00Z"),
	}}

	t.Run("typed id", func(t *testing.T) {
		projectID, stdout, _ := runChooseCapellaSharedProject(t, fake, "n", "not-an-id", testTypedProjectID)
		assert.Equal(t, testTypedProjectID, projectID)
		assert.NotContains(t, stdout, "projects carry this name")
		assert.Contains(t, stdout, `capella project id "not-an-id" is not a UUID`)
	})

	t.Run("empty id", func(t *testing.T) {
		projectID, _, _ := runChooseCapellaSharedProject(t, fake, "n", "")
		assert.Empty(t, projectID)
	})

	assert.Empty(t, fake.createRequests())
}

func TestChooseCapellaSharedProjectCreates(t *testing.T) {
	fake := &fakeCapellaProjects{projects: []map[string]any{
		{"id": "p-other", "name": "someone-else"},
		{"id": "p-lower", "name": "cbdc2_shared"},
	}}

	projectID, stdout, script := runChooseCapellaSharedProject(t, fake, "y")

	assert.Equal(t, testCreatedProjectID, projectID)
	assert.Equal(t, []string{"Could not find a CBDC2_SHARED project. Create one and use it?"},
		script.questions)
	assert.Contains(t, stdout,
		"Created the shared Capella project CBDC2_SHARED ("+testCreatedProjectID+").\n")

	creates := fake.createRequests()
	require.Len(t, creates, 1)
	assert.Equal(t, "CBDC2_SHARED", creates[0].Name)
	assert.Equal(t, "Shared project for cbdinocluster clusters", creates[0].Description)
}

func TestChooseCapellaSharedProjectNotCreatedByDefault(t *testing.T) {
	fake := &fakeCapellaProjects{}

	t.Run("empty id", func(t *testing.T) {
		projectID, _, script := runChooseCapellaSharedProject(t, fake, "", "")
		assert.Empty(t, projectID)
		assert.Equal(t, []string{
			"Could not find a CBDC2_SHARED project. Create one and use it?",
			"What Capella project ID should clusters go into?",
		}, script.questions)
	})

	t.Run("typed id", func(t *testing.T) {
		projectID, _, _ := runChooseCapellaSharedProject(t, fake, "n", testTypedProjectID)
		assert.Equal(t, testTypedProjectID, projectID)
	})

	assert.Empty(t, fake.createRequests())
}

func TestChooseCapellaSharedProjectListFails(t *testing.T) {
	fake := &fakeCapellaProjects{listStatus: http.StatusForbidden}

	projectID, stdout, script := runChooseCapellaSharedProject(t, fake, testTypedProjectID)

	assert.Equal(t, testTypedProjectID, projectID)
	assert.Equal(t, []string{"What Capella project ID should clusters go into?"}, script.questions)
	assert.Contains(t, stdout, "Failed to look up the CBDC2_SHARED project.\n")
	assert.Contains(t, stdout, "stub failure")
}
