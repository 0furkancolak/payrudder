//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// invoicesOf returns a customer's invoices of one kind.
func invoicesOf(t *testing.T, key, customer, kind string) []map[string]any {
	t.Helper()
	r := call(t, "GET", "/v1/invoices?page_size=100&customer="+customer, key, nil)
	if r.status != 200 {
		t.Fatalf("list invoices: %d %v", r.status, r.body)
	}
	var out []map[string]any
	items, _ := r.body["invoices"].([]any)
	for _, it := range items {
		if inv := it.(map[string]any); inv["kind"] == kind {
			out = append(out, inv)
		}
	}
	return out
}

// TestAdvanceFeeAndUpgradeEndToEnd follows a subscription billed in advance through the real services: the fee for the first period is
// invoiced, charged at the mock PSP and paid without any period being closed; an upgrade is previewed and made, and the difference is invoiced and paid.
func TestAdvanceFeeAndUpgradeEndToEnd(t *testing.T) {
	a := newAccount(t, "TENANT_TIER_STANDARD")
	k := a.sandboxKey
	for _, p := range []struct {
		code string
		fee  int
	}{{"adv_basic", 1000}, {"adv_pro", 4000}} {
		if r := call(t, "POST", "/v1/plans", k, map[string]any{"code": p.code, "name": p.code}); r.status != 201 {
			t.Fatalf("plan %s: %d %v", p.code, r.status, r.body)
		}
		if r := call(t, "POST", "/v1/plans/"+p.code+"/versions", k, map[string]any{"base_fee_minor": p.fee}); r.status != 201 {
			t.Fatalf("version %s: %d %v", p.code, r.status, r.body)
		}
	}
	if r := call(t, "POST", "/v1/customers", k, map[string]any{"external_id": "cus_adv", "name": "Adv", "email": "cus_adv@example.com", "payment_method": "pm_sandbox_success"}); r.status != 201 {
		t.Fatalf("customer: %d %v", r.status, r.body)
	}
	sub := call(t, "POST", "/v1/subscriptions", k, map[string]any{"customer_external_id": "cus_adv", "plan_code": "adv_basic"})
	if sub.status != 201 || sub.body["fee_timing"] != "FEE_TIMING_ADVANCE" {
		t.Fatalf("subscription: %d %v; a new subscription is billed in advance", sub.status, sub.body)
	}
	id := sub.body["id"].(string)

	// The scheduler (every minute in the stack) invoices the first fee; the saga charges it.
	waitFee := func(count int, what string) []map[string]any {
		t.Helper()
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			done := 0
			list := invoicesOf(t, k, "cus_adv", "FEE")
			for _, inv := range list {
				if inv["saga_state"] == "COMPLETED" && inv["status"] == "paid" {
					done++
				}
			}
			if done >= count && len(list) == count {
				return list
			}
			time.Sleep(3 * time.Second)
		}
		t.Fatalf("%s: %d paid fee invoice(s) did not appear within 4 minutes: %v", what, count, invoicesOf(t, k, "cus_adv", "FEE"))
		return nil
	}
	first := waitFee(1, "first fee")
	if first[0]["total_minor"] != "1000" || first[0]["currency"] != "TRY" {
		t.Fatalf("first fee invoice = %v; want 1000 TRY", first[0])
	}

	// An upgrade is previewed, made, and what it moved is invoiced.
	pv := call(t, "POST", "/v1/subscriptions/"+id+"/change-preview", k, map[string]any{"plan_code": "adv_pro"})
	if pv.status != 200 || pv.body["timing"] != "CHANGE_TIMING_IMMEDIATELY" {
		t.Fatalf("preview: %d %v", pv.status, pv.body)
	}
	ch := call(t, "POST", "/v1/subscriptions/"+id+"/change", k, map[string]any{"plan_code": "adv_pro"})
	if ch.status != 200 || ch.body["scheduled"] == true {
		t.Fatalf("change: %d %v", ch.status, ch.body)
	}
	quote := ch.body["quote"].(map[string]any)
	net, _ := quote["net_minor"].(string)
	if net == "" || net == "0" || net[0] == '-' {
		t.Fatalf("an upgrade should cost something now: %v", quote)
	}
	if now := ch.body["subscription"].(map[string]any); now["plan_code"] != "adv_pro" || now["changed_from_id"] != id {
		t.Fatalf("subscription after the change = %v", now)
	}
	both := waitFee(2, "upgrade difference")
	found := false
	for _, inv := range both {
		if inv["total_minor"] == net {
			found = true
		}
	}
	if !found {
		t.Fatalf("no paid fee invoice of %s among %v", net, both)
	}
}
