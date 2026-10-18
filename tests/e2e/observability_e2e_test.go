//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var (
	tempoURL      = envOr("QUOTIFY_TEMPO_URL", "http://localhost:3200")
	prometheusURL = envOr("QUOTIFY_PROMETHEUS_URL", "http://localhost:9090")
)

func getBytes(u string) (int, []byte) {
	resp, err := httpc.Get(u)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// walk calls fn for every JSON object in v.
func walk(v any, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, c := range x {
			walk(c, fn)
		}
	case []any:
		for _, c := range x {
			walk(c, fn)
		}
	}
}

// traceSummary returns the service names and span names of a Tempo trace, or nil while Tempo doesn't have it.
func traceSummary(traceID string) (services, spans map[string]bool) {
	code, b := getBytes(tempoURL + "/api/traces/" + traceID)
	if code != 200 {
		return nil, nil
	}
	var doc any
	if json.Unmarshal(b, &doc) != nil {
		return nil, nil
	}
	services, spans = map[string]bool{}, map[string]bool{}
	walk(doc, func(o map[string]any) {
		if o["key"] == "service.name" {
			if v, ok := o["value"].(map[string]any); ok {
				if s, ok := v["stringValue"].(string); ok {
					services[s] = true
				}
			}
		}
		if n, ok := o["name"].(string); ok && o["spanId"] != nil {
			spans[n] = true
		}
	})
	return services, spans
}

func waitTrace(t *testing.T, traceID string, within time.Duration, ok func(services, spans map[string]bool) bool) (map[string]bool, map[string]bool) {
	t.Helper()
	var svcs, spans map[string]bool
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if svcs, spans = traceSummary(traceID); svcs != nil && ok(svcs, spans) {
			return svcs, spans
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("trace %s did not reach the expected shape within %s: services=%v spans=%v", traceID, within, svcs, spans)
	return nil, nil
}

// TestTraceFollowsAnEventThroughThePipeline sends one debug-marked request with a client-supplied traceparent and
// expects that one trace id to show up in the gateway, ingest-svc (request, relay), aggregator, limits-svc and
// analytics-svc: HTTP → gRPC → Postgres outbox → Kafka → every consumer.
func TestTraceFollowsAnEventThroughThePipeline(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "seats", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "seats"}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}

	raw := make([]byte, 24)
	_, _ = rand.Read(raw)
	traceID, spanID := hex.EncodeToString(raw[:16]), hex.EncodeToString(raw[16:])
	body, _ := json.Marshal(map[string]any{"events": []any{event("trace-1", "seats", 1, time.Now().Add(-time.Minute))}})
	req, _ := http.NewRequest("POST", gateway+"/v1/events", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+k)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", fmt.Sprintf("00-%s-%s-01", traceID, spanID))
	req.Header.Set("X-Quotify-Debug", "1")
	resp, err := httpc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 202 {
		t.Fatalf("events: %d", resp.StatusCode)
	}

	// Tail sampling holds a trace for decision_wait (10 s) before it reaches Tempo.
	want := []string{"gateway", "ingest-svc", "aggregator", "limits-svc", "analytics-svc"}
	svcs, spans := waitTrace(t, traceID, 120*time.Second, func(svcs, _ map[string]bool) bool {
		for _, w := range want {
			if !svcs[w] {
				return false
			}
		}
		return true
	})
	for _, w := range []string{"POST /v1/events", "usage.events.v1 process"} {
		if !spans[w] {
			t.Errorf("trace has no %q span; spans=%v services=%v", w, spans, svcs)
		}
	}
}

// TestSagaTraceAndMetrics runs a sandbox invoice and checks that one trace shows the saga hop by hop (render, charge, the
// replies), then that Prometheus has the metric names docs/08-observability.md promises and no forbidden labels.
func TestSagaTraceAndMetrics(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	setupSandbox(t, a, map[string]string{"cus_e2e": "pm_sandbox_success"})
	evs := []any{event("", "seats", 1, time.Now().Add(-time.Minute))}
	if r := call(t, "POST", "/v1/events", k, map[string]any{"events": evs}); r.status != 202 {
		t.Fatalf("events: %d %v", r.status, r.body)
	}
	if r := call(t, "POST", "/v1/sandbox/close-period", k, nil); r.status != 202 {
		t.Fatalf("close-period: %d %v", r.status, r.body)
	}
	waitInvoices(t, k, []string{"cus_e2e"}, 4*time.Minute)

	// A saga trace: any trace holding saga.transition spans and both worker hops.
	var traceID string
	deadline := time.Now().Add(150 * time.Second)
	for traceID == "" && time.Now().Before(deadline) {
		q := url.QueryEscape(`{ name = "saga.transition" }`)
		if code, b := getBytes(tempoURL + "/api/search?limit=20&q=" + q); code == 200 {
			var res struct {
				Traces []struct {
					TraceID string `json:"traceID"`
				} `json:"traces"`
			}
			_ = json.Unmarshal(b, &res)
			for _, tr := range res.Traces {
				if _, spans := traceSummary(tr.TraceID); spans["saga.transition"] && hasPrefixSpan(spans, "invoice.render") && hasPrefixSpan(spans, "payment.charge") {
					traceID = tr.TraceID
					break
				}
			}
		}
		if traceID == "" {
			time.Sleep(3 * time.Second)
		}
	}
	if traceID == "" {
		t.Fatal("no trace with saga.transition, invoice.render and payment.charge spans reached Tempo")
	}
	svcs, _ := traceSummary(traceID)
	for _, w := range []string{"billing-svc", "invoice-worker", "payments-worker"} {
		if !svcs[w] {
			t.Errorf("saga trace %s lacks service %s: %v", traceID, w, svcs)
		}
	}

	// Metrics: names (after at least one scrape) and the label allow-list.
	required := []string{
		"ingest_events_total", "ingest_outbox_depth", "ingest_outbox_oldest_age_seconds", "relay_publish_duration_seconds_bucket",
		"consumer_lag_records", "consumer_lag_seconds", "aggregator_events_applied_total",
		"saga_transitions_total", "saga_state", "billing_runs", "billing_run_wait_watermark_seconds_bucket", "payments_total",
	}
	var missing []string
	for end := time.Now().Add(90 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Second) {
		code, b := getBytes(prometheusURL + "/api/v1/label/__name__/values")
		if code != 200 {
			continue
		}
		have := map[string]bool{}
		var res struct{ Data []string }
		_ = json.Unmarshal(b, &res)
		for _, n := range res.Data {
			have[n] = true
		}
		missing = missing[:0]
		for _, n := range required {
			if !have[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) == 0 {
			break
		}
	}
	if len(missing) > 0 {
		t.Errorf("metrics missing from Prometheus: %v", missing)
	}
	code, b := getBytes(prometheusURL + "/api/v1/labels")
	if code != 200 {
		t.Fatalf("prometheus labels: %d", code)
	}
	var labels struct{ Data []string }
	_ = json.Unmarshal(b, &labels)
	for _, l := range labels.Data {
		switch l {
		case "customer", "customer_id", "customer_external_id", "event_id", "idempotency_key", "invoice_id":
			t.Errorf("metric label %q is unbounded (docs/08-observability.md#metrics): customers and events belong in traces and logs", l)
		}
	}
}

func hasPrefixSpan(spans map[string]bool, prefix string) bool {
	for s := range spans {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}
