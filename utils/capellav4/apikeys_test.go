package capellav4

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListApiKeysFollowsEveryPage(t *testing.T) {
	const totalItems = 150

	var requests atomic.Int32
	var gotPath, gotAuth string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		assert.Equal(t, http.MethodGet, r.Method)

		perPage, err := strconv.Atoi(r.URL.Query().Get("perPage"))
		require.NoError(t, err)
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		require.NoError(t, err)

		lastPage := (totalItems + perPage - 1) / perPage

		var data []*ApiKeyInfo
		start := (page - 1) * perPage
		for i := start; i < start+perPage && i < totalItems; i++ {
			data = append(data, &ApiKeyInfo{
				ID:                strconv.Itoa(i),
				Name:              "cbdino_pool_test_" + strconv.Itoa(i),
				Expiry:            ExpiryNever,
				OrganizationRoles: []string{OrganizationRoleOwner},
			})
		}

		_ = json.NewEncoder(w).Encode(pagedResponse[*ApiKeyInfo]{
			Data:   data,
			Cursor: cursor{Pages: pageInfo{Page: page, PerPage: perPage, Last: lastPage, TotalItems: totalItems}},
		})
	}))

	apiKeys, err := client.ListApiKeys(context.Background(), "org")
	require.NoError(t, err)

	require.Len(t, apiKeys, totalItems)
	assert.Equal(t, "/v4/organizations/org/apikeys", gotPath)
	assert.Equal(t, "Bearer test-secret", gotAuth)
	assert.Equal(t, "0", apiKeys[0].ID)
	assert.Equal(t, "cbdino_pool_test_0", apiKeys[0].Name)
	assert.Equal(t, float64(-1), apiKeys[0].Expiry)
	assert.Equal(t, []string{OrganizationRoleOwner}, apiKeys[0].OrganizationRoles)
	assert.Equal(t, "149", apiKeys[totalItems-1].ID)
	assert.Equal(t, int32(2), requests.Load())
}

func TestListApiKeysDecodesEveryField(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{
			"id":"key-1",
			"name":"cbdino_pool_dev_abcd1234",
			"description":"Created by cbdinocluster",
			"expiry":-1,
			"allowedCIDRs":["0.0.0.0/0"],
			"organizationRoles":["organizationOwner"],
			"audit":{"createdBy":"someone","createdAt":"2026-01-01T00:00:00Z","version":1}
		}],"cursor":{"pages":{"page":1,"last":1,"perPage":100,"totalItems":1}}}`))
	}))

	apiKeys, err := client.ListApiKeys(context.Background(), "org")
	require.NoError(t, err)

	require.Len(t, apiKeys, 1)
	assert.Equal(t, "key-1", apiKeys[0].ID)
	assert.Equal(t, "cbdino_pool_dev_abcd1234", apiKeys[0].Name)
	assert.Equal(t, "Created by cbdinocluster", apiKeys[0].Description)
	assert.Equal(t, []string{"0.0.0.0/0"}, apiKeys[0].AllowedCIDRs)
	assert.Equal(t, "someone", apiKeys[0].Audit.CreatedBy)
}

func TestCreateApiKey(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var body map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"key-1","token":"the-secret"}`))
	}))

	resp, err := client.CreateApiKey(context.Background(), "org", &CreateApiKeyRequest{
		Name:              "cbdino_pool_dev_abcd1234",
		Description:       "Created by cbdinocluster",
		Expiry:            ExpiryNever,
		OrganizationRoles: []string{OrganizationRoleOwner},
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v4/organizations/org/apikeys", gotPath)
	assert.Equal(t, "Bearer test-secret", gotAuth)
	assert.Equal(t, "cbdino_pool_dev_abcd1234", body["name"])
	assert.Equal(t, "Created by cbdinocluster", body["description"])
	assert.Equal(t, float64(-1), body["expiry"])
	assert.Equal(t, []any{"organizationOwner"}, body["organizationRoles"])
	assert.NotContains(t, body, "allowedCIDRs")

	assert.Equal(t, "key-1", resp.ID)
	assert.Equal(t, "the-secret", resp.Token)
}

func TestDeleteApiKey(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))

	err := client.DeleteApiKey(context.Background(), "org", "key-1")
	require.NoError(t, err)

	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/v4/organizations/org/apikeys/key-1", gotPath)
	assert.Equal(t, "Bearer test-secret", gotAuth)
}

func TestRotateApiKey(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody []byte
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)

		_, _ = w.Write([]byte(`{"secretKey":"key-1","token":"the-new-secret"}`))
	}))

	resp, err := client.RotateApiKey(context.Background(), "org", "key-1")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v4/organizations/org/apikeys/key-1/rotate", gotPath)
	assert.Equal(t, "Bearer test-secret", gotAuth)
	// Capella picks the new secret itself when the request carries no body.
	assert.Empty(t, gotBody)

	assert.Equal(t, "key-1", resp.SecretKey)
	assert.Equal(t, "the-new-secret", resp.Token)
}

func TestApiKeyErrorsAreDecoded(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":1002,"hint":"needs owner","httpStatusCode":403,"message":"access denied"}`))
	}))

	_, err := client.ListApiKeys(context.Background(), "org")
	require.Error(t, err)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.HttpStatusCode)
	assert.Equal(t, "access denied", apiErr.Message)
}
