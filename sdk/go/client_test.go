package payrudder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewRequiresAnAPIKey(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	if _, err := New(); err == nil {
		t.Fatal("New without a key succeeded")
	}
}

func TestNewReadsTheEnvironment(t *testing.T) {
	t.Setenv(EnvAPIKey, "qk_test_envkey")
	t.Setenv(EnvBaseURL, "https://api.example.test/")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if c.apiKey != "qk_test_envkey" || c.baseURL != "https://api.example.test" {
		t.Fatalf("key %q base %q", c.apiKey, c.baseURL)
	}
	// An explicit option wins over the environment.
	c, err = New(WithAPIKey("qk_live_x"), WithBaseURL("http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	if c.apiKey != "qk_live_x" || c.baseURL != "http://127.0.0.1:1" {
		t.Fatalf("key %q base %q", c.apiKey, c.baseURL)
	}
}

func TestNewDefaults(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	c, err := New(WithAPIKey("qk_test_x"))
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "http://localhost:42800" || c.timeout != 30*time.Second || c.maxRetries != 2 || c.userAgent != "payrudder-go/0.0.0" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestNewRejectsABadBaseURL(t *testing.T) {
	for _, u := range []string{"ftp://x", "localhost:42800", "://", "http://"} {
		if _, err := New(WithAPIKey("k"), WithBaseURL(u)); err == nil {
			t.Errorf("base URL %q accepted", u)
		}
	}
}

func TestRequestHeadersAndPath(t *testing.T) {
	var got *http.Request
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{"id":"1","external_id":"a/b c"}`))
	}), WithUserAgent("custom/1"))
	cust, err := c.Customers.GetCustomer(context.Background(), "a/b c")
	if err != nil {
		t.Fatal(err)
	}
	if cust.ExternalID != "a/b c" {
		t.Fatalf("customer %+v", cust)
	}
	if got.Header.Get("Authorization") != "Bearer qk_test_abcdefgh_secret" {
		t.Errorf("authorization %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get("User-Agent") != "custom/1" || got.Header.Get("Accept") != "application/json" {
		t.Errorf("headers %v", got.Header)
	}
	if got.URL.EscapedPath() != "/v1/customers/a%2Fb%20c" {
		t.Errorf("path %q", got.URL.EscapedPath())
	}
}

func TestQueryParameters(t *testing.T) {
	var q url.Values
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte(`{}`))
	}))
	_, err := c.Customers.ListCustomers(context.Background(), &ListCustomersParams{PageSize: Ptr(25), PageToken: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("page_size") != "25" || q.Get("page_token") != "tok" {
		t.Errorf("query %v", q)
	}
	// A nil params struct sends no query at all.
	if _, err := c.Customers.ListCustomers(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(q) != 0 {
		t.Errorf("query %v", q)
	}
}

func TestRequestBodyAndMoneyAsStrings(t *testing.T) {
	var body []byte
	var ct string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		ct = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"link_id":"l1","token":"t","url":"u"}`))
	}))
	_, err := c.PaymentLinks.CreatePaymentLink(context.Background(), &CreatePaymentLinkRequest{
		Kind:        "adhoc",
		AmountMinor: Ptr(Int64(9007199254740993)), // beyond float64 precision
		Currency:    "TRY",
	}, WithIdempotencyKey("k1"))
	if err != nil {
		t.Fatal(err)
	}
	if ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	if !strings.Contains(string(body), `"amount_minor":"9007199254740993"`) {
		t.Errorf("amount not sent as an exact string: %s", body)
	}
}

func TestInt64AndDecimal(t *testing.T) {
	var v struct {
		A Int64   `json:"a"`
		B Int64   `json:"b"`
		C Int64   `json:"c"`
		D Decimal `json:"d"`
		E Decimal `json:"e"`
	}
	if err := json.Unmarshal([]byte(`{"a":"9223372036854775807","b":-5,"c":null,"d":"1.250","e":1.50}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 9223372036854775807 || v.B != -5 || v.C != 0 || v.D != "1.250" || v.E != "1.50" {
		t.Fatalf("%+v", v)
	}
	out, _ := json.Marshal(v)
	if string(out) != `{"a":"9223372036854775807","b":"-5","c":"0","d":"1.250","e":"1.50"}` {
		t.Fatalf("%s", out)
	}
	if err := json.Unmarshal([]byte(`{"a":"12x"}`), &v); err == nil {
		t.Fatal("bad int64 accepted")
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		kind   ErrorKind
		is     *Error
	}{
		{401, KindAuthentication, ErrAuthentication},
		{403, KindPermission, ErrPermission},
		{404, KindNotFound, ErrNotFound},
		{400, KindValidation, ErrValidation},
		{422, KindValidation, ErrValidation},
		{409, KindConflict, ErrConflict},
		{413, KindAPI, nil},
		{429, KindRateLimit, ErrRateLimit},
		{500, KindServer, ErrServer},
		{503, KindServer, ErrServer},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-Id", "hdr-req")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":"INVALID_ARGUMENT","key":"billing.credit_note.nothing_to_refund","message":"nothing to refund","details":[{"field":"amount","description":"too big"}],"request_id":"env-req"}}`))
			}), WithMaxRetries(0))
			_, err := c.Customers.GetCustomer(context.Background(), "x")
			var perr *Error
			if !errors.As(err, &perr) {
				t.Fatalf("not a *Error: %v", err)
			}
			if perr.Status != tc.status || perr.Kind != tc.kind || perr.Code != "INVALID_ARGUMENT" ||
				perr.Key != "billing.credit_note.nothing_to_refund" || perr.Message != "nothing to refund" ||
				perr.RequestID != "env-req" || len(perr.Details) != 1 || perr.Details[0].Field != "amount" {
				t.Fatalf("error %+v", perr)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("errors.Is(%v) is false", tc.is.Kind)
			}
			if tc.is != nil && errors.Is(err, ErrConnection) {
				t.Error("matches the wrong sentinel")
			}
			if !strings.Contains(err.Error(), "billing.credit_note.nothing_to_refund") {
				t.Errorf("message %q", err.Error())
			}
		})
	}
}

func TestErrorWithoutEnvelope(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "from-header")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>bad gateway</html>"))
	}), WithMaxRetries(0))
	_, err := c.Customers.GetCustomer(context.Background(), "x")
	var perr *Error
	if !errors.As(err, &perr) || perr.Status != 502 || perr.RequestID != "from-header" || !strings.Contains(perr.Message, "bad gateway") {
		t.Fatalf("%v", err)
	}
}

func TestRetryOn503ThenSuccess(t *testing.T) {
	var n atomic.Int32
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"code":"UNAVAILABLE","key":"common.unavailable","message":"later"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"1"}`))
	}))
	if _, err := c.Customers.GetCustomer(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 || len(rec.got()) != 1 {
		t.Fatalf("calls %d sleeps %v", n.Load(), rec.got())
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	var n atomic.Int32
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":"RESOURCE_EXHAUSTED","key":"gateway.ratelimit","message":"slow down"}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	if _, err := c.Customers.GetCustomer(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	s := rec.got()
	if len(s) != 2 || s[0] != 3*time.Second || s[1] != 3*time.Second {
		t.Fatalf("sleeps %v", s)
	}
}

func TestRetryAfterBeyondTheCapIsNotWaitedFor(t *testing.T) {
	var n atomic.Int32
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	_, err := c.Customers.GetCustomer(context.Background(), "x")
	var perr *Error
	if !errors.As(err, &perr) || perr.RetryAfter != time.Hour {
		t.Fatalf("%v", err)
	}
	if n.Load() != 1 || len(rec.got()) != 0 {
		t.Fatalf("calls %d sleeps %v", n.Load(), rec.got())
	}
}

func TestRetriesAreBounded(t *testing.T) {
	var n atomic.Int32
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
	}), WithMaxRetries(3))
	_, err := c.Customers.GetCustomer(context.Background(), "x")
	if !errors.Is(err, ErrServer) {
		t.Fatalf("%v", err)
	}
	if n.Load() != 4 || len(rec.got()) != 3 {
		t.Fatalf("calls %d sleeps %v", n.Load(), rec.got())
	}
}

func TestBackoffIsExponentialAndCapped(t *testing.T) {
	c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
	}), WithMaxRetries(5), WithRetryBackoff(100*time.Millisecond, 500*time.Millisecond))
	_, _ = c.Customers.GetCustomer(context.Background(), "x")
	// Jitter is 0, so each delay is half the capped exponential step.
	want := []time.Duration{50, 100, 200, 250, 250}
	got := rec.got()
	if len(got) != len(want) {
		t.Fatalf("sleeps %v", got)
	}
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			t.Fatalf("sleeps %v", got)
		}
	}
}

func TestJitterStaysInsideTheStep(t *testing.T) {
	c, err := New(WithAPIKey("k"), WithRetryBackoff(time.Second, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		d, ok := c.retryDecision(&Error{Kind: KindServer, Status: 503}, 2, 5, true)
		if !ok || d < 2*time.Second || d > 4*time.Second {
			t.Fatalf("delay %v", d)
		}
	}
}

func TestOtherClientErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 413, 422} {
		var n atomic.Int32
		c, rec := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n.Add(1)
			w.WriteHeader(status)
		}))
		_, _ = c.Customers.GetCustomer(context.Background(), "x")
		if n.Load() != 1 || len(rec.got()) != 0 {
			t.Errorf("status %d: calls %d", status, n.Load())
		}
	}
}

func TestStatus500RetryDependsOnIdempotency(t *testing.T) {
	cases := []struct {
		name  string
		call  func(c *Client) error
		calls int32
	}{
		{"GET", func(c *Client) error { _, err := c.Customers.GetCustomer(context.Background(), "x"); return err }, 3},
		{"PUT", func(c *Client) error {
			_, err := c.Customers.SetCustomerAddress(context.Background(), "x", &Address{})
			return err
		}, 3},
		{"DELETE", func(c *Client) error { return c.WebhookEndpoints.DeleteWebhookEndpoint(context.Background(), "id") }, 3},
		{"POST without key", func(c *Client) error {
			_, err := c.Customers.CreateCustomer(context.Background(), &CreateCustomerRequest{ExternalID: "a"})
			return err
		}, 1},
		{"POST with key", func(c *Client) error {
			_, err := c.Customers.CreateCustomer(context.Background(), &CreateCustomerRequest{ExternalID: "a"}, WithIdempotencyKey("k"))
			return err
		}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				w.WriteHeader(500)
			}))
			_ = tc.call(c)
			if n.Load() != tc.calls {
				t.Fatalf("calls %d, want %d", n.Load(), tc.calls)
			}
		})
	}
}

func TestPostWithoutKeyStillRetriesOn503(t *testing.T) {
	var n atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
	}))
	_, _ = c.Customers.CreateCustomer(context.Background(), &CreateCustomerRequest{ExternalID: "a"})
	if n.Load() != 3 {
		t.Fatalf("calls %d", n.Load())
	}
}

func TestIdempotencyKeyAndBodyAreStableAcrossRetries(t *testing.T) {
	var keys, bodies []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		bodies = append(bodies, string(b))
		if len(keys) < 3 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{}`))
	}))
	_, err := c.PaymentLinks.CreatePaymentLink(context.Background(), &CreatePaymentLinkRequest{Kind: "adhoc"}, WithIdempotencyKey("stable-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || keys[0] != "stable-1" || keys[1] != "stable-1" || keys[2] != "stable-1" {
		t.Fatalf("keys %v", keys)
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] || bodies[0] == "" {
		t.Fatalf("bodies %v", bodies)
	}
}

func TestNetworkErrorsAreRetried(t *testing.T) {
	var n atomic.Int32
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if n.Add(1) < 3 {
			return nil, errors.New("connection reset by peer")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`)), Request: r}, nil
	})
	c, err := New(WithAPIKey("k"), WithBaseURL("http://gateway.test"), WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	c.sleep = rec.sleep
	cust, err := c.Customers.GetCustomer(context.Background(), "x")
	if err != nil || cust.ID != "ok" {
		t.Fatalf("%v %v", cust, err)
	}
	if n.Load() != 3 || len(rec.got()) != 2 {
		t.Fatalf("calls %d", n.Load())
	}
}

func TestNetworkFailureBecomesAConnectionError(t *testing.T) {
	boom := errors.New("dial refused")
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, boom })
	c, err := New(WithAPIKey("k"), WithBaseURL("http://gateway.test"), WithTransport(rt), WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Customers.GetCustomer(context.Background(), "x")
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindConnection || perr.Status != 0 || !errors.Is(err, ErrConnection) || !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
}

func TestAttemptTimeout(t *testing.T) {
	release := make(chan struct{})
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), WithTimeout(30*time.Millisecond), WithMaxRetries(0))
	defer close(release)
	_, err := c.Customers.GetCustomer(context.Background(), "x")
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindConnection || !perr.Timeout {
		t.Fatalf("%+v", err)
	}
}

func TestContextCancellationStopsRetrying(t *testing.T) {
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		cancel()
		w.WriteHeader(503)
	}), WithMaxRetries(5))
	_, err := c.Customers.GetCustomer(ctx, "x")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	if n.Load() != 1 {
		t.Fatalf("calls %d", n.Load())
	}
}

func TestRequestOptions(t *testing.T) {
	var h http.Header
	var n atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h = r.Header
		n.Add(1)
		w.WriteHeader(503)
	}))
	_, _ = c.Customers.GetCustomer(context.Background(), "x", WithRequestHeader("X-Trace", "abc"), WithRequestMaxRetries(0), WithRequestTimeout(time.Second))
	if h.Get("X-Trace") != "abc" || n.Load() != 1 {
		t.Fatalf("header %v calls %d", h, n.Load())
	}
}

func TestNoContentAnswer(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/webhook-endpoints/we_1" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(204)
	}))
	if err := c.WebhookEndpoints.DeleteWebhookEndpoint(context.Background(), "we_1"); err != nil {
		t.Fatal(err)
	}
}

func TestRawBodyDownload(t *testing.T) {
	var accept string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	}))
	b, err := c.Accounting.ExportAccountingCsv(context.Background(), &ExportAccountingCsvParams{From: "2026-01-01"})
	if err != nil || string(b) != "a,b\n1,2\n" || accept != "*/*" {
		t.Fatalf("%q %v accept %q", b, err, accept)
	}
}

func TestCancelSubscriptionReadsBothShapes(t *testing.T) {
	bodies := []string{
		`{"id":"s1","status":"canceled"}`,
		`{"subscription":{"id":"s1","status":"canceled"},"credited_minor":"1250"}`,
	}
	var i int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(bodies[i]))
		i++
	}))
	plain, err := c.Subscriptions.CancelSubscription(context.Background(), "s1", nil)
	if err != nil || plain.ID != "s1" || plain.Credited {
		t.Fatalf("%+v %v", plain, err)
	}
	credited, err := c.Subscriptions.CancelSubscription(context.Background(), "s1", &CancelSubscriptionParams{CreditUnused: Ptr(true)})
	if err != nil || credited.ID != "s1" || !credited.Credited || credited.CreditedMinor != 1250 {
		t.Fatalf("%+v %v", credited, err)
	}
}

func TestUploadAssetsBuildsMultipart(t *testing.T) {
	var meta, filename, ctype, content string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		meta = r.FormValue("metadata")
		fh := r.MultipartForm.File["file"][0]
		filename, ctype = fh.Filename, fh.Header.Get("Content-Type")
		f, _ := fh.Open()
		b, _ := io.ReadAll(f)
		content = string(b)
		_, _ = w.Write([]byte(`{"results":[{"filename":"logo.png","status":"created","asset":{"id":"a1"}}]}`))
	}))
	out, err := c.Assets.UploadAssets(context.Background(), []AssetFile{{
		Filename: "logo.png", ContentType: "image/png", Purpose: AssetPurposeInvoiceLogo, Content: strings.NewReader("PNGDATA"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if meta != `{"files":{"logo.png":{"purpose":"invoice_logo"}}}` || filename != "logo.png" || ctype != "image/png" || content != "PNGDATA" {
		t.Fatalf("meta %q file %q %q %q", meta, filename, ctype, content)
	}
	if len(out.Results) != 1 || out.Results[0].Asset.ID != "a1" {
		t.Fatalf("%+v", out)
	}
}

func TestDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}), WithMaxRetries(0))
	_, err := c.Customers.GetCustomer(context.Background(), "x")
	var perr *Error
	if !errors.As(err, &perr) || perr.Status != http.StatusFound || hits.Load() != 1 {
		t.Fatalf("%v hits %d", err, hits.Load())
	}
}
