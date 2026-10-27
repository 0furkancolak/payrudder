// Package payrudder is the Go client of the Payrudder tenant API (/v1/*) and a verifier for webhook deliveries.
//
// It has no dependency outside the standard library. Create a Client with New, call the operations through its
// resource namespaces (client.Customers, client.Events, client.Invoices, ...), and handle failures with errors.As on
// *Error:
//
//	client, err := payrudder.New(payrudder.WithAPIKey("qk_test_..."))
//	cust, err := client.Customers.GetCustomer(ctx, "cus_42")
//	var perr *payrudder.Error
//	if errors.As(err, &perr) && perr.Kind == payrudder.KindNotFound { ... }
//
// The version is 0.0.0 until the platform is released: nothing is published yet.
package payrudder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Version is the SDK version. It stays 0.0.0 (pre-release) until Payrudder is released.
const Version = "0.0.0"

// Defaults and environment variables.
const (
	DefaultBaseURL    = "http://localhost:42800"
	DefaultTimeout    = 30 * time.Second
	DefaultMaxRetries = 2
	// EnvAPIKey is read when no API key is given.
	EnvAPIKey = "PAYRUDDER_API_KEY" //nolint:gosec // the name of a variable, not a credential
	// EnvBaseURL is read when no base URL is given.
	EnvBaseURL = "PAYRUDDER_BASE_URL"

	// maxRetryAfter is the longest Retry-After the client sleeps through; a longer one is returned to the caller.
	maxRetryAfter = 60 * time.Second
	maxErrorBody  = 1 << 20
)

// service is the base of every resource namespace.
type service struct{ c *Client }

// Client talks to one Payrudder gateway with one API key. It is safe for concurrent use.
type Client struct {
	services

	apiKey      string
	baseURL     string
	userAgent   string
	timeout     time.Duration
	maxRetries  int
	backoffBase time.Duration
	backoffMax  time.Duration
	transport   http.RoundTripper
	http        *http.Client

	// sleep and jitter are replaced in tests.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func() float64
}

// Option configures New.
type Option func(*Client)

// WithAPIKey sets the API key (qk_test_... or qk_live_...). The default is $PAYRUDDER_API_KEY.
func WithAPIKey(key string) Option { return func(c *Client) { c.apiKey = key } }

// WithBaseURL sets the gateway address. The default is $PAYRUDDER_BASE_URL, else http://localhost:42800.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithTimeout sets the timeout of one attempt (default 30 s). A retry gets a fresh timeout; use the context to bound a
// whole call.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithMaxRetries sets how many times a failed call is repeated (default 2). Zero turns retries off.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithTransport sets the http.RoundTripper, for tests and for custom TLS, proxy or tracing.
func WithTransport(rt http.RoundTripper) Option { return func(c *Client) { c.transport = rt } }

// WithUserAgent replaces the User-Agent header (default payrudder-go/0.0.0).
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithRetryBackoff sets the first and the largest delay of the exponential backoff (default 500 ms and 8 s).
func WithRetryBackoff(base, max time.Duration) Option {
	return func(c *Client) { c.backoffBase, c.backoffMax = base, max }
}

// New builds a Client. It fails when there is no API key or the base URL is not an http(s) address.
func New(opts ...Option) (*Client, error) {
	c := &Client{
		timeout:     DefaultTimeout,
		maxRetries:  DefaultMaxRetries,
		backoffBase: 500 * time.Millisecond,
		backoffMax:  8 * time.Second,
		userAgent:   "payrudder-go/" + Version,
		sleep:       sleepCtx,
		jitter:      rand.Float64,
	}
	for _, o := range opts {
		o(c)
	}
	if c.apiKey == "" {
		c.apiKey = os.Getenv(EnvAPIKey)
	}
	if c.apiKey == "" {
		return nil, errNoAPIKey
	}
	if c.baseURL == "" {
		c.baseURL = os.Getenv(EnvBaseURL)
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("payrudder: base URL %q is not an http(s) address", c.baseURL)
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	if c.maxRetries < 0 {
		c.maxRetries = 0
	}
	if c.timeout <= 0 {
		c.timeout = DefaultTimeout
	}
	rt := c.transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	// No client-level timeout and no redirects: the per-attempt timeout is a context, and the API never redirects
	// (a redirect would carry the key to another host).
	c.http = &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	c.initServices(c)
	return c, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RequestOption changes one call.
type RequestOption func(*requestConfig)

type requestConfig struct {
	timeout        time.Duration
	maxRetries     int
	header         http.Header
	idempotencyKey string
}

// WithIdempotencyKey sends the Idempotency-Key header. The same key is used by every automatic retry of the call, and a
// POST that carries one is also retried after a 500.
func WithIdempotencyKey(key string) RequestOption {
	return func(rc *requestConfig) { rc.idempotencyKey = key }
}

// WithRequestHeader adds a header to this call.
func WithRequestHeader(key, value string) RequestOption {
	return func(rc *requestConfig) { rc.header.Add(key, value) }
}

// WithRequestTimeout overrides the per-attempt timeout for this call.
func WithRequestTimeout(d time.Duration) RequestOption {
	return func(rc *requestConfig) { rc.timeout = d }
}

// WithRequestMaxRetries overrides the retry count for this call.
func WithRequestMaxRetries(n int) RequestOption { return func(rc *requestConfig) { rc.maxRetries = n } }

type requestSpec struct {
	method string
	path   string
	query  url.Values
	body   any // marshalled as JSON when not nil
	// rawBody replaces body (multipart uploads).
	rawBody     []byte
	contentType string
	// idempotent marks a POST that is safe to repeat even without an Idempotency-Key header (ingest carries one per
	// event).
	idempotent bool
}

func (rs requestSpec) safeToRepeat(rc *requestConfig) bool {
	switch rs.method {
	case http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead:
		return true
	}
	return rs.idempotent || rc.idempotencyKey != ""
}

// do sends the request and decodes a 2xx answer into out (nil: ignore the body; *[]byte: the raw body).
func (c *Client) do(ctx context.Context, rs requestSpec, out any, opts []RequestOption) error {
	rc := &requestConfig{timeout: c.timeout, maxRetries: c.maxRetries, header: http.Header{}}
	for _, o := range opts {
		o(rc)
	}
	var payload []byte
	ct := rs.contentType
	switch {
	case rs.rawBody != nil:
		payload = rs.rawBody
	case rs.body != nil:
		b, err := json.Marshal(rs.body)
		if err != nil {
			return fmt.Errorf("payrudder: encode request: %w", err)
		}
		payload, ct = b, "application/json"
	}
	target := c.baseURL + rs.path
	if len(rs.query) > 0 {
		target += "?" + rs.query.Encode()
	}
	repeat := rs.safeToRepeat(rc)
	accept := "application/json"
	if _, raw := out.(*[]byte); raw {
		accept = "*/*" // CSV and file downloads
	}

	for attempt := 0; ; attempt++ {
		status, header, body, err := c.attempt(ctx, rs.method, target, ct, accept, payload, rc)
		var apiErr *Error
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return newConnectionError(ctx.Err())
			}
			apiErr = newConnectionError(err)
		case status >= 200 && status < 300:
			return decodeBody(out, body)
		default:
			apiErr = newAPIError(status, header, body)
		}
		wait, retry := c.retryDecision(apiErr, attempt, rc.maxRetries, repeat)
		if !retry {
			return apiErr
		}
		if err := c.sleep(ctx, wait); err != nil {
			return newConnectionError(err)
		}
	}
}

// retryDecision applies the retry policy: connection errors, 429, 502, 503 and 504 are retried; 500 only when the
// call is safe to repeat; every other status is final.
func (c *Client) retryDecision(e *Error, attempt, maxRetries int, repeat bool) (time.Duration, bool) {
	if attempt >= maxRetries {
		return 0, false
	}
	switch {
	case e.Kind == KindConnection:
	case e.Status == http.StatusTooManyRequests, e.Status == http.StatusBadGateway,
		e.Status == http.StatusServiceUnavailable, e.Status == http.StatusGatewayTimeout:
	case e.Status == http.StatusInternalServerError && repeat:
	default:
		return 0, false
	}
	if e.RetryAfter > maxRetryAfter {
		return 0, false
	}
	if e.RetryAfter > 0 {
		return e.RetryAfter, true
	}
	d := c.backoffBase << uint(min(attempt, 20))
	if d <= 0 || d > c.backoffMax {
		d = c.backoffMax
	}
	// Equal jitter: half fixed, half random, so a crowd of clients spreads out without waiting nothing.
	return d/2 + time.Duration(c.jitter()*float64(d/2)), true
}

func (c *Client) attempt(ctx context.Context, method, target, contentType, accept string, payload []byte, rc *requestConfig) (int, http.Header, []byte, error) {
	actx, cancel := context.WithTimeout(ctx, rc.timeout)
	defer cancel()
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(actx, method, target, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, vs := range rc.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if rc.idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", rc.idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	limit := int64(1 << 30)
	if resp.StatusCode >= 300 {
		limit = maxErrorBody
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, resp.Header, b, nil
}

func decodeBody(out any, body []byte) error {
	switch o := out.(type) {
	case nil:
		return nil
	case *[]byte:
		*o = body
		return nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("payrudder: decode response: %w", err)
	}
	return nil
}
