//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

func usageQuantity(t *testing.T, key, meter string) string {
	t.Helper()
	r := call(t, "GET", "/v1/customers/cus_e2e/usage", key, nil)
	rows, _ := r.body["usage"].([]any)
	for _, x := range rows {
		row := x.(map[string]any)
		if row["meter_code"] == meter {
			return fmt.Sprint(row["quantity"])
		}
	}
	return ""
}

func waitUsage(t *testing.T, key, meter, want string) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if usageQuantity(t, key, meter) == want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("usage of %s = %q after 40 s, want %s", meter, usageQuantity(t, key, meter), want)
}

// TestReplayRebuildsPromotesAndDiscards runs the replay against the real Kafka and Postgres of the compose stack: a tenant's
// aggregates are rebuilt from the log into generation 2, the verification shows no difference, the generation is promoted,
// events keep counting, and a second replay is discarded.
func TestReplayRebuildsPromotesAndDiscards(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.liveKey
	for _, step := range []struct {
		path string
		body map[string]any
	}{
		{"/v1/meters", map[string]any{"code": "calls", "aggregation": "AGGREGATION_COUNT", "quantity_scale": 0, "unit_label": "calls"}},
		{"/v1/customers", map[string]any{"external_id": "cus_e2e", "name": "E2E", "email": "e2e@example.com"}},
	} {
		if r := call(t, "POST", step.path, k, step.body); r.status != 201 {
			t.Fatalf("POST %s: %d %v", step.path, r.status, r.body)
		}
	}
	send := func(prefix string, n int) {
		evs := make([]any, n)
		for i := range evs {
			evs[i] = event(fmt.Sprintf("%s-%d", prefix, i), "calls", 1, time.Now().Add(-time.Minute))
		}
		if r := call(t, "POST", "/v1/events", k, map[string]any{"events": evs}); r.status != 202 {
			t.Fatalf("events: %d %v", r.status, r.body)
		}
	}
	send("before", 50)
	waitUsage(t, k, "calls", "50")

	loc, _ := time.LoadLocation("Europe/Istanbul")
	period := time.Now().In(loc).Format("2006-01")
	base := "/admin/v1/tenants/" + a.live + "/replays"
	start := call(t, "POST", base, operatorToken, map[string]any{"from_period_id": period, "reason": "e2e"})
	if start.status != 201 || start.body["target_generation"] != float64(2) {
		t.Fatalf("start replay: %d %v", start.status, start.body)
	}
	id := start.body["id"].(string)
	if r := call(t, "POST", base, operatorToken, map[string]any{"from_period_id": period, "reason": "again"}); r.status != 409 {
		t.Fatalf("a second open replay: %d %v, want 409", r.status, r.body)
	}
	send("during", 10) // live traffic while the replay runs: it must be counted once

	var job result
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		job = call(t, "GET", base+"/"+id, operatorToken, nil)
		if job.body["status"] == "REPLAY_STATUS_READY" || job.body["status"] == "REPLAY_STATUS_FAILED" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if job.body["status"] != "REPLAY_STATUS_READY" {
		t.Fatalf("replay = %d %v, want READY", job.status, job.body)
	}
	ver, _ := job.body["verification"].(map[string]any)
	if ver["rows_changed"] != "0" && ver["rows_changed"] != nil || ver["rows_only_in_old"] != "0" && ver["rows_only_in_old"] != nil ||
		ver["rows_only_in_new"] != "0" && ver["rows_only_in_new"] != nil {
		t.Fatalf("verification = %v: the log was replayed with the same rules, nothing should differ", ver)
	}

	promoted := call(t, "POST", base+"/"+id+"/promote", operatorToken, nil)
	if promoted.status != 200 || promoted.body["status"] != "REPLAY_STATUS_PROMOTED" {
		t.Fatalf("promote: %d %v", promoted.status, promoted.body)
	}
	if again := call(t, "POST", base+"/"+id+"/promote", operatorToken, nil); again.status != 200 {
		t.Fatalf("promoting twice: %d %v, want 200", again.status, again.body)
	}
	waitUsage(t, k, "calls", "60")
	send("after", 5)
	waitUsage(t, k, "calls", "65")

	// A second replay can be started and abandoned; the live generation (2) keeps counting.
	second := call(t, "POST", base, operatorToken, map[string]any{"from_period_id": period, "reason": "e2e discard"})
	if second.status != 201 {
		t.Fatalf("second replay: %d %v", second.status, second.body)
	}
	if d := call(t, "POST", base+"/"+second.body["id"].(string)+"/discard", operatorToken, nil); d.status != 200 || d.body["status"] != "REPLAY_STATUS_DISCARDED" {
		t.Fatalf("discard: %d %v", d.status, d.body)
	}
	send("last", 5)
	waitUsage(t, k, "calls", "70")

	// Replay is an operator tool.
	if r := call(t, "POST", base, k, map[string]any{"from_period_id": period, "reason": "x"}); r.status != 401 && r.status != 403 {
		t.Fatalf("a tenant key reached the replay API: %d", r.status)
	}
}
