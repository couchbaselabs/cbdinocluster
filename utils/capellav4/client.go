// Package capellav4 is a client for the public Capella Management API v4.
//
// Unlike the internal v2 API in utils/capellacontrol, authentication here is a
// stateless organization API key. There is no session to establish and none to
// invalidate, so concurrent cbdinocluster runs that share one credential no
// longer log each other out.
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
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// DefaultEndpoint is the public v4 API base URL. It is a different host from
// the internal v2 API, which does not accept API key credentials.
const DefaultEndpoint = "https://cloudapi.cloud.couchbase.com"

// MaxPerPage is the largest page size the v4 API accepts. Larger values are
// rejected with HTTP 400 rather than being clamped.
const MaxPerPage = 100

const maxReadRetries = 10

type Client struct {
	logger     *zap.Logger
	httpClient *http.Client
	endpoint   string
	secretKey  string
}

type ClientOptions struct {
	Logger     *zap.Logger
	HttpClient *http.Client
	// Endpoint defaults to DefaultEndpoint when empty.
	Endpoint string
	// SecretKey is the secret half of an organization API key. The access key
	// is not part of v4 authentication and is not sent.
	SecretKey string
}

func NewClient(opts *ClientOptions) (*Client, error) {
	if opts == nil {
		return nil, errors.New("client options must be specified")
	}
	if opts.SecretKey == "" {
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
		secretKey:  opts.SecretKey,
	}, nil
}

// Error is the v4 API error envelope. It differs from the v2 shape, which used
// error, errorType and message.
type Error struct {
	Code           int    `json:"code"`
	Hint           string `json:"hint"`
	HttpStatusCode int    `json:"httpStatusCode"`
	Message        string `json:"message"`

	// FullText is the raw body, kept for errors that are not valid JSON.
	FullText string `json:"-"`
}

var _ error = (*Error)(nil)

func (e *Error) Error() string {
	return fmt.Sprintf("capella v4 error (status: %d, code: %d): %s (hint: %s)",
		e.HttpStatusCode, e.Code, e.Message, e.Hint)
}

// IsNotFound reports whether err is a v4 error for a missing resource. Callers
// use this to tell "deleted" apart from "request failed" when polling.
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
		// Transport level failures are always worth another attempt.
		return true
	}

	switch {
	case apiErr.HttpStatusCode == http.StatusRequestTimeout:
		return true
	case apiErr.HttpStatusCode == http.StatusTooManyRequests:
		return true
	case apiErr.HttpStatusCode >= 500:
		return true
	default:
		return false
	}
}

func (c *Client) doOnce(ctx context.Context, method, path string, body, out any) error {
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
	req.Header.Set("Authorization", "Bearer "+c.secretKey)

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

// doRead issues an idempotent request and retries transient failures.
func (c *Client) doRead(ctx context.Context, method, path string, body, out any) error {
	for retryNum := 0; ; retryNum++ {
		err := c.doOnce(ctx, method, path, body, out)
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

// doWrite issues a mutating request exactly once. Retrying a create or a delete
// risks acting twice on the same resource, so failures propagate immediately.
func (c *Client) doWrite(ctx context.Context, method, path string, body, out any) error {
	return c.doOnce(ctx, method, path, body, out)
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

// listAll walks every page of a paginated collection. The v4 API caps perPage
// at 100, so any collection that can exceed that needs this rather than a
// single oversized request.
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

		// An empty collection reports last as 0, so treat "no more rows" and
		// "reached the final page" the same way.
		if len(resp.Data) == 0 || page >= resp.Cursor.Pages.Last {
			return out, nil
		}
	}
}
