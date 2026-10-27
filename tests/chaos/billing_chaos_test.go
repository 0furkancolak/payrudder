//go:build chaos

package chaos

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

var mockPSPURL = envOr("MOCKPSP_URL", "http://localhost:42890")

// TestMonthCloseSurvivesBrokerRestart is the "RabbitMQ restart during the month close" scenario
// (docs/09-testing-strategy.md#additional-chaos-scenarios): a sandbox period is closed for many customers, RabbitMQ is
// restarted while their invoice sagas are mid-flight, and every saga must still reach COMPLETED with exactly the
// invoice total, one successful charge each (A5, A6) and nothing in a dead-letter queue.
func TestMonthCloseSurvivesBrokerRestart(t *testing.T) {
	const customers = 60
	a := api{t}
	var acct struct {
		Tenants []struct {
			ID          string `json:"id"`
			Environment string `json:"environment"`
		} `json:"tenants"`
	}
	a.must("POST", "/admin/v1/accounts", operatorToken, map[string]any{
		"name": "chaos-billing-" + fmt.Sprint(time.Now().UnixNano()), "currency": "TRY", "timezone": "Europe/Istanbul",
		"grace_period": "0s", "tier": "TENANT_TIER_ENTERPRISE"}, &acct, 201)
	var sandbox string
	for _, tn := range acct.Tenants {
		if tn.Environment == "ENVIRONMENT_SANDBOX" {
			sandbox = tn.ID
		}
	}
	var key struct {
		Plaintext string `json:"plaintext"`
	}
	a.must("POST", "/admin/v1/tenants/"+sandbox+"/api-keys", operatorToken, nil, &key, 201)
	k := key.Plaintext

	a.must("POST", "/v1/meters", k, map[string]any{"code": "seats", "aggregation": "AGGREGATION_SUM", "value_property": "n", "quantity_scale": 0, "unit_label": "seats"}, nil, 201)
	a.must("POST", "/v1/plans", k, map[string]any{"code": "team", "name": "Team"}, nil, 201)
	a.must("POST", "/v1/plans/team/versions", k, map[string]any{"base_fee_minor": 1000,
		"prices": []any{map[string]any{"meter_code": "seats", "per_unit": map[string]any{"unit_price": "1"}}}}, nil, 201)
	wantTotal := map[string]int64{}
	var events []any
	for i := 0; i < customers; i++ {
		ext := fmt.Sprintf("cus_%d", i)
		a.must("POST", "/v1/customers", k, map[string]any{"external_id": ext, "name": ext, "email": ext + "@example.com", "payment_method": "pm_sandbox_success"}, nil, 201)
		a.must("POST", "/v1/subscriptions", k, map[string]any{"customer_external_id": ext, "plan_code": "team", "fee_timing": "FEE_TIMING_ARREARS"}, nil, 201)
		units := int64(1 + i%7)
		wantTotal[ext] = 1000 + units // base fee + 1 minor unit per seat
		events = append(events, map[string]any{"customer_external_id": ext, "meter_code": "seats", "quantity": fmt.Sprint(units),
			"event_time": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), "idempotency_key": "bill-" + ext})
	}
	a.must("POST", "/v1/events", k, map[string]any{"events": events}, nil, 202)
	// Close the period only once the aggregator holds every seat. ClosePeriodEarly waits for the rows its consumer batches hold, and the gateway gives the
	// call 5 s (grpcx.DefaultClientTimeout): on a slow runner right after the stack came up (the aggregator crashes after a commit by design here) the call
	// answered 504 with the events still in flight (ci run 40516273222). A usage still in flight at the cutoff would also be late and miss its invoice.
	waitForUsage(t, a, k, customers)
	a.must("POST", "/v1/sandbox/close-period", k, nil, nil, 202)

	// Restart RabbitMQ as soon as the first invoice exists, i.e. while the sagas are in flight.
	restarted := false
	deadline := time.Now().Add(10 * time.Minute)
	type invoice struct {
		ID, Customer, Status, SagaState, Total string
	}
	var invs map[string]invoice
	for time.Now().Before(deadline) {
		var list struct {
			Invoices []struct {
				ID        string `json:"id"`
				Customer  string `json:"customer_external_id"`
				Status    string `json:"status"`
				SagaState string `json:"saga_state"`
				Total     string `json:"total_minor"`
			} `json:"invoices"`
		}
		a.call("GET", "/v1/invoices?page_size=100", k, nil, &list)
		invs = map[string]invoice{}
		done := 0
		for _, i := range list.Invoices {
			invs[i.Customer] = invoice{i.ID, i.Customer, i.Status, i.SagaState, i.Total}
			if i.SagaState == "COMPLETED" {
				done++
			}
		}
		if !restarted && len(invs) > 0 && done < customers {
			restarted = true
			if err := newPlatform().restartAll("rabbitmq"); err != nil {
				t.Fatalf("restart rabbitmq: %v", err)
			}
		}
		if len(invs) == customers && done == customers {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !restarted {
		t.Fatal("the invoices completed before RabbitMQ could be restarted: the scenario did not run")
	}
	var bad []string
	for i := 0; i < customers; i++ {
		ext := fmt.Sprintf("cus_%d", i)
		inv, ok := invs[ext]
		switch {
		case !ok:
			bad = append(bad, ext+": no invoice")
			continue
		case inv.SagaState != "COMPLETED" || inv.Status != "paid":
			bad = append(bad, fmt.Sprintf("%s: saga %s, invoice %s", ext, inv.SagaState, inv.Status))
		case inv.Total != fmt.Sprint(wantTotal[ext]):
			bad = append(bad, fmt.Sprintf("A5 %s: total %s, want %d", ext, inv.Total, wantTotal[ext]))
		}
		var ch struct {
			Charges []struct {
				Status string `json:"status"`
			} `json:"charges"`
		}
		if resp, err := httpc.Get(mockPSPURL + "/v1/charges?invoice_id=" + url.QueryEscape(inv.ID)); err == nil {
			_ = jsonDecode(resp.Body, &ch)
			_ = resp.Body.Close()
		}
		ok2 := 0
		for _, c := range ch.Charges {
			if c.Status == "succeeded" {
				ok2++
			}
		}
		if ok2 != 1 {
			bad = append(bad, fmt.Sprintf("A6 %s: %d successful charges, want exactly 1", ext, ok2))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d problems after the RabbitMQ restart:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// waitForUsage waits until the aggregator has counted the one seats event of each of the first n customers (cus_0 ... cus_n-1).
func waitForUsage(t *testing.T, a api, key string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	missing := n
	for time.Now().Before(deadline) {
		missing = 0
		for i := 0; i < n; i++ {
			var u struct {
				Usage []struct {
					EventCount string `json:"event_count"`
				} `json:"usage"`
			}
			if a.call("GET", fmt.Sprintf("/v1/customers/cus_%d/usage", i), key, nil, &u) != 200 || len(u.Usage) != 1 || u.Usage[0].EventCount != "1" {
				missing++
			}
		}
		if missing == 0 {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("the aggregator counted the seats event of %d of %d customers within 2 minutes", n-missing, n)
}
