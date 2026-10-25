package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// fakeIngest records what the gateway forwarded and answers by event content.
type fakeIngest struct {
	ingestv1.UnimplementedIngestServiceServer
	mu     sync.Mutex
	last   *ingestv1.IngestEventsRequest
	tenant tenant.Info
	err    error
}

func (f *fakeIngest) IngestEvents(ctx context.Context, req *ingestv1.IngestEventsRequest) (*ingestv1.IngestEventsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = req
	f.tenant, _ = tenant.FromContext(ctx)
	if f.err != nil {
		return nil, f.err
	}
	resp := &ingestv1.IngestEventsResponse{}
	for i, e := range req.Events {
		switch e.MeterCode {
		case "dup":
			resp.Results = append(resp.Results, &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_DUPLICATE, EventId: "orig"})
		case "bad":
			resp.Results = append(resp.Results, &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_REJECTED, ErrorCode: "UNKNOWN_METER", ErrorMessage: "no"})
		default:
			resp.Results = append(resp.Results, &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_ACCEPTED, EventId: fmt.Sprintf("ev%d", i)})
		}
	}
	return resp, nil
}

func TestIngestEventsMapsRequestAndResponse(t *testing.T) {
	ts, _, ing, lim := newFullTestServer(t)
	body := `{"events":[
	  {"idempotency_key":"k1","customer_external_id":"cus_1","meter_code":"gb","quantity":"1.250","event_time":"2026-11-03T10:15:00Z","properties":{"region":"eu"}},
	  {"idempotency_key":"k2","customer_external_id":"cus_1","meter_code":"dup","quantity":1.10,"event_time":"2026-11-03T10:15:00.5+03:00"},
	  {"idempotency_key":"k3","customer_external_id":"cus_1","meter_code":"bad","quantity":3,"event_time":"2026-11-03T10:15:00Z"}]}`
	resp, m := do(t, ts, "POST", "/v1/events", "live-key", body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d %v", resp.StatusCode, m)
	}
	results := m["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("results = %v", results)
	}
	r0, r1, r2 := results[0].(map[string]any), results[1].(map[string]any), results[2].(map[string]any)
	if r0["status"] != "accepted" || r0["event_id"] != "ev0" || r1["status"] != "duplicate" || r1["event_id"] != "orig" ||
		r2["status"] != "rejected" || r2["error_code"] != "UNKNOWN_METER" {
		t.Fatalf("results = %v", results)
	}

	// Quantities arrive as the text the client wrote: a JSON number is never turned into a float.
	got := ing.last.Events
	if got[0].Quantity != "1.250" || got[1].Quantity != "1.10" || got[2].Quantity != "3" {
		t.Fatalf("quantities = %q %q %q", got[0].Quantity, got[1].Quantity, got[2].Quantity)
	}
	if got[0].Properties["region"] != "eu" || got[0].IdempotencyKey != "k1" || got[0].EventTime.AsTime().Year() != 2026 {
		t.Fatalf("event 0 = %v", got[0])
	}
	if got[1].EventTime.AsTime().UTC().String() != "2026-11-03 07:15:00.5 +0000 UTC" {
		t.Fatalf("offset timestamp = %s", got[1].EventTime.AsTime().UTC())
	}
	if ing.tenant.ID != liveTenant || ing.tenant.Environment != "live" {
		t.Fatalf("ingest-svc saw tenant %+v", ing.tenant)
	}
	// One unit per event, charged to the ingest class, and nothing else.
	if c := lim.costs[ratelimit.ClassIngest]; len(c) != 1 || c[0] != 3 {
		t.Fatalf("ingest charges = %v, want [3]", c)
	}
}

func TestIngestEventsBadRequests(t *testing.T) {
	ts, _, ing, lim := newFullTestServer(t)
	ev := `{"customer_external_id":"c","meter_code":"m","quantity":"1","event_time":"2026-11-03T10:15:00Z"}`
	many := `{"events":[` + strings.Repeat(ev+",", MaxEventsPerRequest) + ev + `]}`
	cases := []struct {
		name, body string
		status     int
		code       string
	}{
		{"empty body", ``, 400, "INVALID_ARGUMENT"},
		{"no events", `{"events":[]}`, 400, "INVALID_ARGUMENT"},
		{"not json", `{"events":`, 400, "INVALID_ARGUMENT"},
		{"unknown field", `{"events":[{"meter":"m"}]}`, 400, "INVALID_ARGUMENT"},
		{"trailing data", `{"events":[` + ev + `]} {}`, 400, "INVALID_ARGUMENT"},
		{"bad event time", `{"events":[{"customer_external_id":"c","meter_code":"m","quantity":"1","event_time":"yesterday"}]}`, 400, "INVALID_ARGUMENT"},
		{"property not a string", `{"events":[{"properties":{"a":1}}]}`, 400, "INVALID_ARGUMENT"},
		{"501 events", many, 413, "REQUEST_TOO_LARGE"},
		{"over 1 MiB", `{"events":[{"customer_external_id":"` + strings.Repeat("x", MaxBodyBytes) + `"}]}`, 413, "REQUEST_TOO_LARGE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, m := do(t, ts, "POST", "/v1/events", "live-key", c.body)
			if resp.StatusCode != c.status || errCode(m) != c.code {
				t.Fatalf("got %d %v, want %d %s", resp.StatusCode, m, c.status, c.code)
			}
		})
	}
	if ing.last != nil {
		t.Fatal("a rejected request reached ingest-svc")
	}
	if len(lim.costs[ratelimit.ClassIngest]) != 0 {
		t.Fatalf("a malformed request was charged: %v", lim.costs)
	}
	if resp, _ := do(t, ts, "POST", "/v1/events", "", `{}`); resp.StatusCode != 401 {
		t.Fatalf("no key: %d", resp.StatusCode)
	}
}

func TestIngestEventsRateLimitedPerEvent(t *testing.T) {
	ts, _, ing, _ := newFullTestServer(t)
	body := `{"events":[{"customer_external_id":"c","meter_code":"m","quantity":"1","event_time":"2026-11-03T10:15:00Z"}]}`
	resp, m := do(t, ts, "POST", "/v1/events", "limited-key", body)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "2" || errCode(m) != "RESOURCE_EXHAUSTED" {
		t.Fatalf("limited: %d retry=%q %v", resp.StatusCode, resp.Header.Get("Retry-After"), m)
	}
	if ing.last != nil {
		t.Fatal("a rate-limited request reached ingest-svc")
	}
}

func TestIngestEventsUpstreamErrors(t *testing.T) {
	ts, _, ing, _ := newFullTestServer(t)
	body := `{"events":[{"customer_external_id":"c","meter_code":"m","quantity":"1","event_time":"2026-11-03T10:15:00Z"}]}`
	ing.err = status.Error(codes.Unavailable, "db down")
	if resp, m := do(t, ts, "POST", "/v1/events", "live-key", body); resp.StatusCode != 503 || errCode(m) != "UNAVAILABLE" ||
		resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("unavailable: %d retry=%q %v", resp.StatusCode, resp.Header.Get("Retry-After"), m)
	}
	ing.err = status.Error(codes.ResourceExhausted, "backlog")
	resp, m := do(t, ts, "POST", "/v1/events", "live-key", body)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" || errCode(m) != "RESOURCE_EXHAUSTED" {
		t.Fatalf("backlog: %d retry=%q %v", resp.StatusCode, resp.Header.Get("Retry-After"), m)
	}
}
