package capellav4

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	return newTestClientWithKeys(t, handler, "test-secret")
}

func newTestClientWithKeys(t *testing.T, handler http.Handler, secretKeys ...string) *Client {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := NewClient(&ClientOptions{
		Endpoint:   srv.URL,
		SecretKeys: secretKeys,
	})
	require.NoError(t, err)

	return client
}

func TestNewClientRequiresSecret(t *testing.T) {
	_, err := NewClient(&ClientOptions{})
	require.Error(t, err)

	_, err = NewClient(&ClientOptions{SecretKeys: []string{}})
	require.Error(t, err)

	_, err = NewClient(&ClientOptions{SecretKeys: []string{"", "  "}})
	require.Error(t, err)

	_, err = NewClient(nil)
	require.Error(t, err)
}

func TestSendSwapsKeyOnRateLimit(t *testing.T) {
	var gotAuth []string
	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		if len(gotAuth) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"clus"}`))
	}), "key-1", "key-2", "key-3")

	cluster, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	require.NoError(t, err)

	assert.Equal(t, "clus", cluster.ID)
	assert.ElementsMatch(t, []string{"Bearer key-1", "Bearer key-2", "Bearer key-3"}, gotAuth)
}

func TestSendUsesEveryKeyWhenCallersRunConcurrently(t *testing.T) {
	const concurrency = 8
	secretKeys := []string{"key-1", "key-2", "key-3", "key-4"}

	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	gotAuth := make(map[string][]string)
	arrived := 0
	round := 0

	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth[r.URL.Path] = append(gotAuth[r.URL.Path], r.Header.Get("Authorization"))

		// Hold every caller until all of them arrived, so the attempts interleave
		// the way concurrent project inspections do.
		arrived++
		if arrived == concurrency {
			arrived = 0
			round++
			cond.Broadcast()
		} else {
			for myRound := round; round == myRound; {
				cond.Wait()
			}
		}
		mu.Unlock()

		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
	}), secretKeys...)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := fmt.Sprintf("/v4/caller-%d", i)
			err := client.send(context.Background(), http.MethodGet, path, nil, nil)
			assert.True(t, IsRateLimit(err))
		}()
	}
	wg.Wait()

	var wantAuth []string
	for _, secretKey := range secretKeys {
		wantAuth = append(wantAuth, "Bearer "+secretKey)
	}

	require.Len(t, gotAuth, concurrency)
	for path, auths := range gotAuth {
		assert.ElementsMatch(t, wantAuth, auths, "caller %s did not try every key once", path)
	}
}

func TestSendStopsOnNonRateLimitError(t *testing.T) {
	var requests atomic.Int32
	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}), "key-1", "key-2", "key-3")

	err := client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)

	assert.Equal(t, int32(1), requests.Load())
}

func TestSendReturnsRateLimitWhenEveryKeyIsLimited(t *testing.T) {
	var requests atomic.Int32
	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
	}), "key-1", "key-2")

	err := client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)

	assert.True(t, IsRateLimit(err))
	assert.Equal(t, int32(2), requests.Load())
}

func TestClientSendsBearerSecret(t *testing.T) {
	var gotAuth string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))

	_, err := client.CreateProject(context.Background(), "org", &CreateProjectRequest{Name: "n"})
	require.NoError(t, err)

	// The v4 API authenticates with the secret alone.
	assert.Equal(t, "Bearer test-secret", gotAuth)
}

func TestListAllFollowsEveryPage(t *testing.T) {
	const totalItems = 250

	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		perPage, err := strconv.Atoi(r.URL.Query().Get("perPage"))
		require.NoError(t, err)
		// The v4 API rejects a perPage above 100.
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
	// An empty collection reports last as 0.
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

	// The v4 API can answer 404 with a body that is not JSON.
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

func TestReadDoesNotRetryRateLimit(t *testing.T) {
	// The key pool is the rate limit strategy, so a 429 fails fast.
	var requests atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
	}))

	_, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.HttpStatusCode)
	assert.Equal(t, int32(1), requests.Load())
}

func TestReadFailsFastWhenEveryKeyIsRateLimited(t *testing.T) {
	secretKeys := []string{"key-1", "key-2", "key-3"}

	var requests atomic.Int32
	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
	}), secretKeys...)

	start := time.Now()
	_, err := client.GetCluster(context.Background(), "org", "proj", "clus")
	elapsed := time.Since(start)
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTooManyRequests, apiErr.HttpStatusCode)

	// One attempt per key, then the caller sees the 429 without a retry wait.
	assert.Equal(t, int32(len(secretKeys)), requests.Load())
	assert.Less(t, elapsed, 500*time.Millisecond)
}

func TestWriteIsNeverRetried(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusRequestTimeout} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var requests atomic.Int32
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
			}))

			_, err := client.CreateProject(context.Background(), "org", &CreateProjectRequest{Name: "n"})
			require.Error(t, err)

			assert.Equal(t, int32(1), requests.Load())
		})
	}
}

func TestCreateClusterOmitsUnsetServerVersionAndCidr(t *testing.T) {
	// Capella chooses the defaults only when these keys are absent entirely.
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

func TestListAnalyticsPrivateEndpointsNormalizesIDs(t *testing.T) {
	// The analytics API reports endpoint IDs as endpointId, not id.
	var gotPath string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"endpoints":[{"endpointId":"vpce-123","status":"pendingAcceptance"}]}`))
	}))

	endpoints, err := client.ListAnalyticsPrivateEndpoints(context.Background(), "org", "proj", "clus")
	require.NoError(t, err)

	assert.Equal(t, "/v4/organizations/org/projects/proj/analyticsClusters/clus/privateEndpointService/endpoints", gotPath)
	require.Len(t, endpoints, 1)
	assert.Equal(t, "vpce-123", endpoints[0].ID)
	assert.Equal(t, PrivateEndpointPendingAcceptance, endpoints[0].Status)
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

func TestAddAndRemoveSecretKeyChangeTheSweep(t *testing.T) {
	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer key-a" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"clus"}`))
	}), "key-a")

	err := client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)
	assert.True(t, IsRateLimit(err))

	client.AddSecretKey("key-b")
	require.NoError(t, client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil))

	// A second add of the same secret must not grow the ring.
	client.AddSecretKey("key-b")
	client.RemoveSecretKey("key-b")
	err = client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)
	assert.True(t, IsRateLimit(err))

	client.RemoveSecretKey("key-a")
	err = client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)
	assert.False(t, IsRateLimit(err))
	assert.Contains(t, err.Error(), "empty")
}

func TestRateLimitErrorExposesRetryAfter(t *testing.T) {
	var retryAfter string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1004,"httpStatusCode":429,"message":"rate limit"}`))
	}))

	retryAfter = "7"
	err := client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)
	secs, ok := RetryAfterSeconds(err)
	assert.True(t, ok)
	assert.Equal(t, 7, secs)

	retryAfter = ""
	err = client.send(context.Background(), http.MethodGet, "/v4/whatever", nil, nil)
	require.Error(t, err)
	_, ok = RetryAfterSeconds(err)
	assert.False(t, ok)
}

func TestRingStaysUsableWhileItChanges(t *testing.T) {
	const senders = 4
	const changers = 2
	const rounds = 20

	client := newTestClientWithKeys(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"clus"}`))
	}), "key-0")

	var wg sync.WaitGroup
	for i := 0; i < senders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				assert.NoError(t, client.send(context.Background(),
					http.MethodGet, "/v4/whatever", nil, nil))
			}
		}()
	}
	for i := 0; i < changers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				secret := fmt.Sprintf("key-%d-%d", i, round)
				client.AddSecretKey(secret)
				client.RemoveSecretKey(secret)
			}
		}()
	}
	wg.Wait()
}
