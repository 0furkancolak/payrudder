package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
)

func TestErrorsCarryAKey(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, http.MethodPost, "/v1/customers", "", `{}`)
	e, _ := m["error"].(map[string]any)
	if resp.StatusCode != http.StatusUnauthorized || e["key"] != "identity.api_key.missing" {
		t.Fatalf("missing api key: %d %v", resp.StatusCode, m)
	}
	// An error that names its own key keeps it.
	resp, m = do(t, ts, http.MethodPost, "/v1/invoices/draft/links", "live-key", "")
	e, _ = m["error"].(map[string]any)
	if resp.StatusCode != http.StatusConflict || e["key"] != "billing.invoice_link.draft_invoice" && e["key"] != "common.failed_precondition" {
		t.Fatalf("draft: %d %v", resp.StatusCode, m)
	}
}

func TestReadsCanBeRevalidated(t *testing.T) {
	ts, _ := newTestServer(t)
	get := func(etag string) *http.Response {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/invoices/inv-1", nil)
		req.Header.Set("Authorization", "Bearer live-key")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	first := get("")
	tag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || !strings.HasPrefix(tag, `W/"`) || first.Header.Get("Cache-Control") != "private, no-cache" || !strings.Contains(first.Header.Get("Vary"), "Authorization") {
		t.Fatalf("first read: %d %v", first.StatusCode, first.Header)
	}
	if again := get(tag); again.StatusCode != http.StatusNotModified {
		t.Errorf("a repeat with the tag: %d", again.StatusCode)
	}
	if other := get(`W/"somethingelse"`); other.StatusCode != http.StatusOK {
		t.Errorf("a different tag: %d", other.StatusCode)
	}
	// A write is never tagged.
	if w, _ := do(t, ts, http.MethodPost, "/v1/customers", "live-key", `{"external_id":"a","name":"A","email":"a@example.com"}`); w.Header.Get("ETag") != "" {
		t.Error("a write response carries an ETag")
	}
}

// The text of a transport or library error names internal hosts, ports and library versions. It is not written for the caller and never reaches the body: the caller
// gets a plain sentence, the request id to quote, and the key (the default one for the code, so a client can still branch on it). An error a service wrote itself with
// a key keeps its message: that is the explanation the caller is meant to read.
func TestRawUpstreamTextOfTheRetryableCodesNeverReachesTheCaller(t *testing.T) {
	raw := map[codes.Code]string{
		codes.Unavailable:       `connection error: desc = "transport: Error while dialing: dial tcp 10.3.4.5:7001: connect: connection refused"`,
		codes.DeadlineExceeded:  "context deadline exceeded while waiting for plans-svc.payrudder.svc.cluster.local:7001",
		codes.ResourceExhausted: "grpc: received message larger than max (9437184 vs. 4194304) from 10.3.4.5:7001",
		codes.Aborted:           `pq: could not serialize access due to concurrent update (relation "invoices", host 10.3.9.9)`,
	}
	for code, text := range raw {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
		req.Header.Set("X-Request-Id", "req-abc-123")
		requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, r, status.Error(code, text)) })).ServeHTTP(rec, req)
		var body ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		e := body.Error
		if rec.Code != statusOf(code) || e.Code != codeName(code) || e.Key != errkey.Default(code) || e.RequestID != "req-abc-123" {
			t.Errorf("%v: status %d, %+v", code, rec.Code, e)
		}
		if strings.Contains(rec.Body.String(), "10.3") || strings.Contains(rec.Body.String(), "dial") || strings.Contains(rec.Body.String(), "pq:") ||
			strings.Contains(rec.Body.String(), "svc.cluster") || strings.Contains(rec.Body.String(), "grpc:") || strings.Contains(rec.Body.String(), "relation") {
			t.Errorf("%v: the upstream text reached the caller: %s", code, rec.Body)
		}
		if e.Message == "" || !strings.Contains(e.Message, "req-abc-123") {
			t.Errorf("%v: the message %q is empty or does not carry the request id", code, e.Message)
		}
	}
	// An error written with a key is the service's own explanation and keeps its words.
	authored := errkey.New(codes.ResourceExhausted, "identity.invite.quota", "too many invitations: revoke some or wait a day")
	rec := httptest.NewRecorder()
	writeError(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil), authored)
	if !strings.Contains(rec.Body.String(), "too many invitations: revoke some or wait a day") || !strings.Contains(rec.Body.String(), "identity.invite.quota") {
		t.Errorf("an authored error lost its message or key: %s", rec.Body)
	}
	// The other codes are unchanged: a caller's mistake says what it was.
	rec = httptest.NewRecorder()
	writeError(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil), status.Error(codes.NotFound, "no such customer"))
	if !strings.Contains(rec.Body.String(), "no such customer") {
		t.Errorf("a not-found lost its message: %s", rec.Body)
	}
}

// The real thing: plans-svc is not there (nothing listens at 127.0.0.1:1). The client library's own dial error, with the address in it, is what the status holds.
func TestAnUnreachableBackendAnswers503WithoutItsAddress(t *testing.T) {
	cc, err := grpcx.NewClient("passthrough:///127.0.0.1:1", grpc.WithDisableRetry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ts, _, _, _ := newTestServerWith(t, func(d *Deps) { d.Plans = plansv1.NewPlansServiceClient(cc) })
	resp, m := do(t, ts, http.MethodPost, "/v1/customers", "live-key", `{"external_id":"a","name":"A","email":"a@example.com"}`)
	raw, _ := json.Marshal(m)
	if resp.StatusCode != http.StatusServiceUnavailable && resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status %d %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), "dial") || strings.Contains(string(raw), "refused") || strings.Contains(string(raw), "transport") {
		t.Errorf("the dial error reached the caller: %s", raw)
	}
	if e, _ := m["error"].(map[string]any); e["request_id"] == "" || !strings.Contains(e["message"].(string), e["request_id"].(string)) {
		t.Errorf("the message does not carry the request id: %v", e)
	}
}
