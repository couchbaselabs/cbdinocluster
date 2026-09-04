// Package capellav4 is a client for the public Capella Management API v4.
// Authentication uses a stateless API key, so there is no session to invalidate.
package capellav4

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

const DefaultEndpoint = "https://cloudapi.cloud.couchbase.com"

// MaxPerPage is the largest page size the v4 API accepts, over returns HTTP 400
const MaxPerPage = 100

const maxReadRetries = 10

type Client struct {
	logger     *zap.Logger
	httpClient *http.Client
	endpoint   string
	nextKey    atomic.Uint64

	keysLock   sync.Mutex
	secretKeys []string
}

type ClientOptions struct {
	Logger     *zap.Logger
	HttpClient *http.Client
	Endpoint   string
	// The v4 API authenticates with the secret alone. The access key is not sent.
	// Capella limits requests per key, so a pool of keys raises the budget.
	SecretKeys []string
}

func NewClient(opts *ClientOptions) (*Client, error) {
	if opts == nil {
		return nil, errors.New("client options must be specified")
	}
	var secretKeys []string
	for _, secretKey := range opts.SecretKeys {
		if secretKey = strings.TrimSpace(secretKey); secretKey != "" {
			secretKeys = append(secretKeys, secretKey)
		}
	}
	if len(secretKeys) == 0 {
		return nil, errors.New("a capella api secret key is required")
	}

	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	httpClient := opts.HttpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	return &Client{
		logger:     logger,
		httpClient: httpClient,
		endpoint:   endpoint,
		secretKeys: secretKeys,
	}, nil
}

// AddSecretKey puts a secret at the end of the ring. A request already in
// flight keeps the ring it started with, the new secret joins the next call.
func (c *Client) AddSecretKey(secret string) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return
	}

	c.keysLock.Lock()
	defer c.keysLock.Unlock()

	for _, secretKey := range c.secretKeys {
		if secretKey == secret {
			return
		}
	}
	c.secretKeys = append(c.secretKeys, secret)
}

// RemoveSecretKey takes a secret out of the ring, keeping the order of the
// rest. A key must leave the ring before it is rotated or deleted, otherwise a
// later call could authorize itself with a secret that no longer works.
func (c *Client) RemoveSecretKey(secret string) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return
	}

	c.keysLock.Lock()
	defer c.keysLock.Unlock()

	kept := c.secretKeys[:0]
	for _, secretKey := range c.secretKeys {
		if secretKey == secret {
			continue
		}
		kept = append(kept, secretKey)
	}
	c.secretKeys = kept
}

func (c *Client) secretKeySnapshot() []string {
	c.keysLock.Lock()
	defer c.keysLock.Unlock()
	return append([]string(nil), c.secretKeys...)
}

type Error struct {
	Code           int    `json:"code"`
	Hint           string `json:"hint"`
	HttpStatusCode int    `json:"httpStatusCode"`
	Message        string `json:"message"`

	FullText string `json:"-"`

	retryAfterSecs int
	hasRetryAfter  bool
}

var _ error = (*Error)(nil)

func (e *Error) Error() string {
	return fmt.Sprintf("capella v4 error (status: %d, code: %d): %s (hint: %s)",
		e.HttpStatusCode, e.Code, e.Message, e.Hint)
}

func IsNotFound(err error) bool {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.HttpStatusCode == http.StatusNotFound
	}
	return false
}

func isRetryable(err error) bool {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		return true
	}

	// A rate limited request is not retried, so a busy window fails fast
	// instead of stalling a run.
	switch {
	case apiErr.HttpStatusCode == http.StatusRequestTimeout:
		return true
	case apiErr.HttpStatusCode >= 500:
		return true
	default:
		return false
	}
}

func IsRateLimit(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.HttpStatusCode == http.StatusTooManyRequests
}

// RetryAfterSeconds reports how long Capella asked the caller to wait. It
// reports false when the answer carried no usable Retry-After header, so the
// caller has to pick its own delay.
func RetryAfterSeconds(err error) (int, bool) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		return 0, false
	}
	return apiErr.retryAfterSecs, apiErr.hasRetryAfter
}

func (c *Client) doOnce(ctx context.Context, secretKey, method, path string, body, out any) error {
	var bodyRdr io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return errors.Wrap(err, "failed to encode request body")
		}
		bodyRdr = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bodyRdr)
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	if bodyRdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+secretKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to execute request")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(resp.Body)

		apiErr := &Error{}
		_ = json.Unmarshal(respBytes, apiErr)
		apiErr.FullText = string(respBytes)
		if apiErr.HttpStatusCode == 0 {
			apiErr.HttpStatusCode = resp.StatusCode
		}
		if apiErr.Message == "" {
			apiErr.Message = string(respBytes)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
				apiErr.retryAfterSecs = secs
				apiErr.hasRetryAfter = true
			}
		}

		return apiErr
	}

	if out == nil {
		return nil
	}

	// Successful mutations often reply 204 with no body.
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.Wrap(err, "failed to read response")
	}
	if len(respBytes) == 0 {
		return nil
	}

	if err := json.Unmarshal(respBytes, out); err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	return nil
}

// send tries the request once per key, so a rate limited key costs a key swap
// rather than a wait.
func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	secretKeys := c.secretKeySnapshot()
	if len(secretKeys) == 0 {
		return errors.New("the capella api key ring is empty, no secret key is left to send with")
	}

	start := c.nextKey.Add(1)

	var err error
	for i := range secretKeys {
		secretKey := secretKeys[(start+uint64(i))%uint64(len(secretKeys))]
		err = c.doOnce(ctx, secretKey, method, path, body, out)
		if !IsRateLimit(err) {
			return err
		}
	}
	return err
}

func (c *Client) doRead(ctx context.Context, method, path string, body, out any) error {
	for retryNum := 0; ; retryNum++ {
		err := c.send(ctx, method, path, body, out)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return err
		}
		if !isRetryable(err) {
			return err
		}
		if retryNum >= maxReadRetries {
			c.logger.Debug("request failed, exhausted retries",
				zap.Error(err), zap.Int("retryNum", retryNum))
			return err
		}

		retryTime := time.Duration(500+retryNum*100) * time.Millisecond
		c.logger.Debug("request failed, retrying",
			zap.Error(err),
			zap.Duration("retryTime", retryTime),
			zap.Int("retryNum", retryNum))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryTime):
		}
	}
}

// A write is never repeated, because a second attempt could provision the resource twice.
// Only a 429 was safe to handle automatically, since the server refused the request before
// doing any work, and the key sweep in send now covers that.
func (c *Client) doWrite(ctx context.Context, method, path string, body, out any) error {
	return c.send(ctx, method, path, body, out)
}

type pageInfo struct {
	Page       int `json:"page"`
	Next       int `json:"next"`
	Previous   int `json:"previous"`
	Last       int `json:"last"`
	PerPage    int `json:"perPage"`
	TotalItems int `json:"totalItems"`
}

type cursor struct {
	Pages pageInfo `json:"pages"`
}

type pagedResponse[T any] struct {
	Data   []T    `json:"data"`
	Cursor cursor `json:"cursor"`
}

func listAll[T any](ctx context.Context, c *Client, path string, params url.Values) ([]T, error) {
	if params == nil {
		params = url.Values{}
	}

	var out []T
	for page := 1; ; page++ {
		query := url.Values{}
		maps.Copy(query, params)
		query.Set("page", strconv.Itoa(page))
		query.Set("perPage", strconv.Itoa(MaxPerPage))

		resp := &pagedResponse[T]{}
		err := c.doRead(ctx, http.MethodGet, path+"?"+query.Encode(), nil, resp)
		if err != nil {
			return nil, err
		}

		out = append(out, resp.Data...)

		// An empty collection reports last as 0.
		if len(resp.Data) == 0 || page >= resp.Cursor.Pages.Last {
			return out, nil
		}
	}
}
