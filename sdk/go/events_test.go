package payrudder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ingestServer accepts every event, except those whose meter_code is "bad", which it rejects. It records each request.
type ingestServer struct {
	mu       sync.Mutex
	requests []IngestEventsRequest
	sizes    []int
	// failFirst makes the first n requests answer 503.
	failFirst int
	calls     int
}

func (s *ingestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls++
	fail := s.calls <= s.failFirst
	var req IngestEventsRequest
	_ = json.Unmarshal(b, &req)
	s.requests = append(s.requests, req)
	s.sizes = append(s.sizes, len(b))
	s.mu.Unlock()
	if fail {
		w.WriteHeader(503)
		return
	}
	var resp IngestEventsResponse
	for i, ev := range req.Events {
		if ev.MeterCode == "bad" {
			resp.Results = append(resp.Results, EventResult{Status: EventRejected, ErrorCode: "UNKNOWN_METER", ErrorMessage: "no such meter"})
			continue
		}
		resp.Results = append(resp.Results, EventResult{Status: EventAccepted, EventID: fmt.Sprintf("ev_%s_%d", ev.CustomerExternalID, i)})
	}
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(resp)
}

func events(n int, mutate func(i int, e *IncomingEvent)) []IncomingEvent {
	out := make([]IncomingEvent, n)
	for i := range out {
		out[i] = IncomingEvent{
			CustomerExternalID: fmt.Sprintf("cus_%d", i),
			MeterCode:          "ai_tokens",
			Quantity:           "1.5",
			EventTime:          time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		}
		if mutate != nil {
			mutate(i, &out[i])
		}
	}
	return out
}

func TestIngestSplitsIntoBatchesOf500AndKeepsOrder(t *testing.T) {
	srv := &ingestServer{}
	c, _ := newTestClient(t, srv)
	in := events(1203, nil)
	res, err := c.Events.Ingest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1203 {
		t.Fatalf("%d results", len(res))
	}
	if len(srv.requests) != 3 || len(srv.requests[0].Events) != 500 || len(srv.requests[1].Events) != 500 || len(srv.requests[2].Events) != 203 {
		t.Fatalf("batches: %d", len(srv.requests))
	}
	// Result i belongs to input i: the fake server encodes the customer in the event id.
	for _, i := range []int{0, 499, 500, 1000, 1202} {
		if want := fmt.Sprintf("ev_cus_%d_%d", i, i%500); res[i].EventID != want {
			t.Errorf("result %d = %q, want %q", i, res[i].EventID, want)
		}
	}
}

func TestIngestFillsUniqueKeysAndLeavesTheCallersSliceAlone(t *testing.T) {
	srv := &ingestServer{}
	c, _ := newTestClient(t, srv)
	in := events(10, func(i int, e *IncomingEvent) {
		if i == 3 {
			e.IdempotencyKey = "mine-3"
		}
	})
	if _, err := c.Events.Ingest(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i, ev := range srv.requests[0].Events {
		if ev.IdempotencyKey == "" || seen[ev.IdempotencyKey] {
			t.Fatalf("event %d key %q", i, ev.IdempotencyKey)
		}
		seen[ev.IdempotencyKey] = true
		if !strings.HasPrefix(ev.IdempotencyKey, "sdk_") && i != 3 {
			t.Errorf("event %d key %q", i, ev.IdempotencyKey)
		}
	}
	if srv.requests[0].Events[3].IdempotencyKey != "mine-3" {
		t.Errorf("the caller's key was replaced")
	}
	for i, ev := range in {
		if i != 3 && ev.IdempotencyKey != "" {
			t.Fatalf("the caller's slice was modified at %d", i)
		}
	}
}

func TestIngestKeysAreStableAcrossRetries(t *testing.T) {
	srv := &ingestServer{failFirst: 2}
	c, _ := newTestClient(t, srv)
	if _, err := c.Events.Ingest(context.Background(), events(3, nil)); err != nil {
		t.Fatal(err)
	}
	if len(srv.requests) != 3 {
		t.Fatalf("calls %d", len(srv.requests))
	}
	for a := 1; a < 3; a++ {
		for i := range srv.requests[0].Events {
			if srv.requests[a].Events[i].IdempotencyKey != srv.requests[0].Events[i].IdempotencyKey {
				t.Fatalf("attempt %d event %d got a new key", a, i)
			}
		}
	}
}

func TestIngestRetriesA500BecauseEveryEventCarriesAKey(t *testing.T) {
	var n int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"results":[{"status":"duplicate","event_id":"ev1"}]}`))
	}))
	res, err := c.Events.Ingest(context.Background(), events(1, nil))
	if err != nil || n != 2 || res[0].Status != EventDuplicate {
		t.Fatalf("calls %d res %+v err %v", n, res, err)
	}
	// A direct batch without keys is not repeated after a 500.
	n = 0
	_, err = c.Events.IngestEvents(context.Background(), &IngestEventsRequest{Events: events(1, nil)})
	if err == nil || n != 1 {
		t.Fatalf("calls %d err %v", n, err)
	}
}

func TestIngestReportsARejectedEventWithoutAnError(t *testing.T) {
	c, _ := newTestClient(t, &ingestServer{})
	res, err := c.Events.Ingest(context.Background(), events(3, func(i int, e *IncomingEvent) {
		if i == 1 {
			e.MeterCode = "bad"
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != EventAccepted || res[1].Status != EventRejected || res[1].ErrorCode != "UNKNOWN_METER" || res[2].Status != EventAccepted {
		t.Fatalf("%+v", res)
	}
}

func TestIngestSplitsBySize(t *testing.T) {
	srv := &ingestServer{}
	c, _ := newTestClient(t, srv)
	big := strings.Repeat("x", 3500) // 300 events of about 3.6 KB are 1.1 MB: two batches of fewer than 500 events
	in := events(300, func(i int, e *IncomingEvent) {
		e.Properties = map[string]string{"blob": big}
	})
	res, err := c.Events.Ingest(context.Background(), in)
	if err != nil || len(res) != 300 {
		t.Fatalf("%d results, %v", len(res), err)
	}
	if len(srv.requests) < 2 {
		t.Fatalf("one request of %d bytes", srv.sizes[0])
	}
	for i, n := range srv.sizes {
		if n > MaxRequestBytes {
			t.Errorf("request %d is %d bytes", i, n)
		}
	}
}

func TestIngestRejectsAnEventThatCannotFitLocally(t *testing.T) {
	srv := &ingestServer{}
	c, _ := newTestClient(t, srv)
	in := events(3, nil)
	in[1].Properties = map[string]string{"blob": strings.Repeat("y", MaxRequestBytes)}
	res, err := c.Events.Ingest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].Status != EventAccepted || res[1].Status != EventRejected || res[1].ErrorCode != ErrCodeEventTooLarge || res[2].Status != EventAccepted {
		t.Fatalf("%+v", res)
	}
}

func TestIngestReturnsEarlierResultsWhenABatchFails(t *testing.T) {
	var n int
	h := &ingestServer{}
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 2 {
			w.WriteHeader(401)
			return
		}
		h.ServeHTTP(w, r)
	}))
	res, err := c.Events.Ingest(context.Background(), events(700, nil))
	if err == nil {
		t.Fatal("no error")
	}
	if len(res) != 500 {
		t.Fatalf("%d results", len(res))
	}
}

func TestIngestEmpty(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a request was sent")
	}))
	res, err := c.Events.Ingest(context.Background(), nil)
	if err != nil || res != nil {
		t.Fatalf("%v %v", res, err)
	}
}

func TestQuantityIsSentAsTheGivenText(t *testing.T) {
	var body string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"results":[{"status":"accepted"}]}`))
	}))
	in := events(1, func(i int, e *IncomingEvent) { e.Quantity = "0.1000000000000000055" })
	if _, err := c.Events.Ingest(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"quantity":"0.1000000000000000055"`) {
		t.Fatalf("%s", body)
	}
}
