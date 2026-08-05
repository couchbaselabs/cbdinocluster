package capellav4

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := NewClient(&ClientOptions{
		Endpoint:  srv.URL,
		SecretKey: "test-secret",
	})
	require.NoError(t, err)

	return client
}

func TestNewClientRequiresSecret(t *testing.T) {
	_, err := NewClient(&ClientOptions{})
	require.Error(t, err)

	_, err = NewClient(nil)
	require.Error(t, err)
}

func TestClientSendsBearerSecret(t *testing.T) {
	var gotAuth string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))

	_, err := client.CreateProject(context.Background(), "org", &CreateProjectRequest{Name: "n"})
	require.NoError(t, err)

	// The access key is not part of v4 auth, only the secret is sent.
	assert.Equal(t, "Bearer test-secret", gotAuth)
}

func TestListAllFollowsEveryPage(t *testing.T) {
	const totalItems = 250

	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		perPage, err := strconv.Atoi(r.URL.Query().Get("perPage"))
		require.NoError(t, err)
		// The v4 API rejects anything above 100, so the client must never ask
		// for more.
		require.LessOrEqual(t, perPage, MaxPerPage)

		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		require.NoError(t, err)

		lastPage := (totalItems + perPage - 1) / perPage

		var data []*ProjectInfo
		start := (page - 1) * perPage
		for i := start; i < start+perPage && i < totalItems; i++ {
			data = append(data, &ProjectInfo{ID: strconv.Itoa(i)})
		}

		_ = json.NewEncoder(w).Encode(pagedResponse[*ProjectInfo]{
			Data:   data,
			Cursor: cursor{Pages: pageInfo{Page: page, PerPage: perPage, Last: lastPage, TotalItems: totalItems}},
		})
	}))

	projects, err := client.ListProjects(context.Background(), "org")
	require.NoError(t, err)

	require.Len(t, projects, totalItems)
	assert.Equal(t, "0", projects[0].ID)
	assert.Equal(t, "249", projects[totalItems-1].ID)
	assert.Equal(t, int32(3), requests.Load())
}

func TestListAllStopsOnEmptyCollection(t *testing.T) {
	// An empty collection reports last as 0, which must not loop forever.
	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"data":[],"cursor":{"pages":{"page":1,"last":0,"perPage":100,"totalItems":0}}}`))
	}))

	projects, err := client.ListProjects(context.Background(), "org")
	require.NoError(t, err)

	assert.Empty(t, projects)
	assert.Equal(t, int32(1), requests.Load())
}

func TestErrorDecoding(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":4000,"hint":"fix it","httpStatusCode":422,"message":"bad plan"}`))
	}))

	_, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 4000, apiErr.Code)
	assert.Equal(t, 422, apiErr.HttpStatusCode)
	assert.Equal(t, "bad plan", apiErr.Message)
	assert.Contains(t, err.Error(), "bad plan")
}

func TestErrorDecodingNonJsonBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`404 page not found`))
	}))

	_, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	require.Error(t, err)

	// A non-JSON body must still yield the right status, since IsNotFound
	// drives the deletion wait.
	assert.True(t, IsNotFound(err))

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "404 page not found", apiErr.Message)
}

func TestIsNotFoundOnOtherErrors(t *testing.T) {
	assert.False(t, IsNotFound(nil))
	assert.False(t, IsNotFound(fmt.Errorf("some transport failure")))
	assert.False(t, IsNotFound(&Error{HttpStatusCode: 500}))
}

func TestReadRetriesOnServerError(t *testing.T) {
	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"id":"clus","currentState":"healthy"}`))
	}))

	cluster, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	require.NoError(t, err)

	assert.Equal(t, StateHealthy, cluster.CurrentState)
	assert.Equal(t, int32(3), requests.Load())
}

func TestReadDoesNotRetryClientError(t *testing.T) {
	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))

	_, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	require.Error(t, err)

	assert.Equal(t, int32(1), requests.Load())
}

func TestWriteIsNeverRetried(t *testing.T) {
	// Retrying a create risks provisioning twice, so writes must be attempted
	// exactly once even on a retryable status.
	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	_, err := client.CreateProject(context.Background(), "org", &CreateProjectRequest{Name: "n"})
	require.Error(t, err)

	assert.Equal(t, int32(1), requests.Load())
}

func TestCreateClusterOmitsUnsetServerVersionAndCidr(t *testing.T) {
	// Capella picks the default server version and allocates a CIDR only when
	// these keys are absent entirely, so an empty object would break it.
	var body map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, _ = w.Write([]byte(`{"id":"clus"}`))
	}))

	_, err := client.CreateCluster(context.Background(), "org", "proj", &CreateClusterRequest{
		Name:          "c",
		CloudProvider: CloudProvider{Type: ProviderAws, Region: "us-west-2"},
		Availability:  Availability{Type: AvailabilityMulti},
		Support:       Support{Plan: "developer pro", Timezone: "PT"},
		ServiceGroups: []ServiceGroup{{
			Node:       Node{Compute: Compute{Cpu: 4, Ram: 16}, Disk: Disk{Type: "gp3", Storage: 50, Iops: 3000}},
			NumOfNodes: 3,
			Services:   []string{"data"},
		}},
	})
	require.NoError(t, err)

	assert.NotContains(t, body, "couchbaseServer")

	provider, ok := body["cloudProvider"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, provider, "cidr")
}

func TestCreateClusterSendsServerVersionWhenSet(t *testing.T) {
	var body map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_, _ = w.Write([]byte(`{"id":"clus"}`))
	}))

	_, err := client.CreateCluster(context.Background(), "org", "proj", &CreateClusterRequest{
		Name:            "c",
		CloudProvider:   CloudProvider{Type: ProviderAws, Region: "us-west-2", Cidr: "10.0.1.0/24"},
		CouchbaseServer: &CouchbaseServer{Version: "7.6.0"},
		Availability:    Availability{Type: AvailabilityMulti},
		Support:         Support{Plan: "developer pro"},
	})
	require.NoError(t, err)

	server, ok := body["couchbaseServer"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "7.6.0", server["version"])

	provider, ok := body["cloudProvider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "10.0.1.0/24", provider["cidr"])
}

func TestUserHasPrivilege(t *testing.T) {
	tests := []struct {
		name      string
		user      UserInfo
		wantRead  bool
		wantWrite bool
	}{
		{
			name:      "no access",
			user:      UserInfo{},
			wantRead:  false,
			wantWrite: false,
		},
		{
			name: "canonical names",
			user: UserInfo{Access: []UserAccess{
				{Privileges: []string{PrivilegeDataReader, PrivilegeDataWriter}},
			}},
			wantRead:  true,
			wantWrite: true,
		},
		{
			name: "aliases",
			user: UserInfo{Access: []UserAccess{
				{Privileges: []string{"read", "write"}},
			}},
			wantRead:  true,
			wantWrite: true,
		},
		{
			name: "read only across several entries",
			user: UserInfo{Access: []UserAccess{
				{Privileges: []string{"unrelated"}},
				{Privileges: []string{PrivilegeDataReader}},
			}},
			wantRead:  true,
			wantWrite: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantRead, tt.user.HasPrivilege(PrivilegeDataReader))
			assert.Equal(t, tt.wantWrite, tt.user.HasPrivilege(PrivilegeDataWriter))
		})
	}
}
