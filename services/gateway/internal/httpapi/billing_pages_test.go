package httpapi

import (
	"strings"
	"testing"
	"time"
)

// What the billing pages of the product app call: invoices, the payments ledger, packages and pricing, reports. All of them are tenant routes, which a
// session reaches by acting as a tenant of its own accounts (X-Quotify-Tenant); a viewer may only read. The operator-only copies stay out of reach.

// tenantReads are the reads the product pages make; none names a tenant in its path or query, so the tenant can only come from the header.
var tenantReads = []string{
	"/v1/invoices", "/v1/invoices?status=open&page_size=50", "/v1/invoices/00000000-0000-4000-8000-000000000001", "/v1/invoices/00000000-0000-4000-8000-000000000001/pdf",
	"/v1/payments", "/v1/payments?limit=20",
	"/v1/plans", "/v1/plans/basic/versions/latest",
	"/v1/coupons", "/v1/tax-rates", "/v1/dunning-settings",
	"/v1/reports/revenue", "/v1/reports/subscriptions", "/v1/reports/dunning",
	"/v1/customers", "/v1/webhook-endpoints", "/v1/payment-links", "/v1/credit-packs",
}

func TestBillingPagesAreNeverReachableFromAnotherAccount(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	for _, path := range tenantReads {
		// Ada asks for Bob's tenants (live and sandbox): the same answer as for a tenant that does not exist.
		for _, bobs := range []string{bobLive, bobSandbox} {
			resp, m := p.call(t, "GET", path, tokAda, bobs, "")
			if resp.StatusCode != 404 || errKeyOf(m) != "identity.tenant.not_found" {
				t.Errorf("GET %s as Ada acting on Bob's %s: %d %v, want 404 identity.tenant.not_found", path, bobs, resp.StatusCode, m)
			}
		}
		// Bob's session on Ada's tenant, the reverse.
		if resp, m := p.call(t, "GET", path, tokBob, adaLive, ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.tenant.not_found" {
			t.Errorf("GET %s as Bob acting on Ada's tenant: %d %v", path, resp.StatusCode, m)
		}
		// No tenant named: a session is not a tenant key.
		if resp, _ := p.call(t, "GET", path, tokAda, "", ""); resp.StatusCode != 401 {
			t.Errorf("GET %s with a session and no tenant: %d, want 401", path, resp.StatusCode)
		}
		// No credential, an expired session, an unknown API key.
		for _, tok := range []string{"", "qs_expired", "no-such-key"} {
			if resp, _ := p.call(t, "GET", path, tok, adaLive, ""); resp.StatusCode != 401 {
				t.Errorf("GET %s with token %q: %d, want 401", path, tok, resp.StatusCode)
			}
		}
	}
	p.ledgerMu.Lock()
	defer p.ledgerMu.Unlock()
	if len(p.ledgerPaths) != 0 {
		t.Errorf("a refused request still read a ledger: %v", p.ledgerPaths)
	}
}

func TestViewersReadBillingPagesButCannotWrite(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	for _, path := range []string{"/v1/payments", "/v1/customers", "/v1/reports/subscriptions"} {
		if resp, m := p.call(t, "GET", path, tokViewer, adaLive, ""); resp.StatusCode != 200 {
			t.Errorf("a viewer reads %s: %d %v", path, resp.StatusCode, m)
		}
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/v1/plans", `{"code":"pro","name":"Pro"}`},
		{"POST", "/v1/plans/pro/versions", `{"base_fee_minor":"100"}`},
		{"POST", "/v1/coupons", `{}`},
		{"DELETE", "/v1/coupons/SAVE10", ""},
		{"PUT", "/v1/tax-rates", `{"rates":[]}`},
		{"PUT", "/v1/dunning-settings", `{"retry_hours":[24]}`},
		{"POST", "/v1/customers", `{"external_id":"c1"}`},
		{"POST", "/v1/invoices/00000000-0000-4000-8000-000000000001/links", `{}`},
	} {
		resp, m := p.call(t, c.method, c.path, tokViewer, adaLive, c.body)
		if resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
			t.Errorf("%s %s as a viewer: %d %v, want 403 identity.account.forbidden", c.method, c.path, resp.StatusCode, m)
		}
	}
}

func TestPaymentsRouteFoldsTheOwnLedgerNewestFirst(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	resp, m := p.call(t, "GET", "/v1/payments", tokAda, adaLive, "")
	if resp.StatusCode != 200 {
		t.Fatalf("payments: %d %v", resp.StatusCode, m)
	}
	list, _ := m["payments"].([]any)
	var order, statuses []string
	for _, x := range list {
		row := x.(map[string]any)
		order = append(order, row["id"].(string))
		statuses = append(statuses, row["status"].(string))
		if _, isString := row["amount_minor"].(string); !isString {
			t.Errorf("amounts are minor-unit strings, got %T", row["amount_minor"])
		}
		if _, leaked := row["provider_ref"]; leaked {
			t.Error("a provider reference must not be passed on")
		}
	}
	// p4 (pending, a minute ago), p1 (pending then succeeded: one paid payment), p2 (declined), r1 (refund), p3 (TRY), old; the payout is not a payment.
	if strings.Join(order, ",") != "5,9,7,4,6,3" || strings.Join(statuses, ",") != "processing,paid,failed,refunded,paid,paid" {
		t.Errorf("order %v statuses %v", order, statuses)
	}
	first := list[1].(map[string]any)
	if first["amount_minor"] != "4900" || first["currency"] != "USD" || first["provider"] != "iyzico" || first["kind"] != "charge" {
		t.Errorf("row = %v", first)
	}
	if m["truncated"] != false {
		t.Errorf("truncated = %v", m["truncated"])
	}
	// The ledger read was the principal's own tenant, whatever else the request says.
	if resp, _ := p.call(t, "GET", "/v1/payments?tenant_id="+bobLive+"&tenant="+bobLive, tokAda, adaLive, ""); resp.StatusCode != 200 {
		t.Fatalf("with a foreign tenant id in the query: %d", resp.StatusCode)
	}
	p.ledgerMu.Lock()
	defer p.ledgerMu.Unlock()
	for _, path := range p.ledgerPaths {
		if path != "/admin/tenants/"+adaLive+"/ledger" {
			t.Errorf("the gateway read the ledger at %s", path)
		}
	}
	if len(p.ledgerPaths) != 2 {
		t.Errorf("ledger reads = %v", p.ledgerPaths)
	}
}

func TestPaymentsRouteLimitsAndFailures(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	if _, m := p.call(t, "GET", "/v1/payments?limit=2", tokAda, adaLive, ""); len(m["payments"].([]any)) != 2 {
		t.Errorf("limit 2: %v", m["payments"])
	}
	for _, bad := range []string{"0", "201", "-1", "abc", "1.5"} {
		if resp, _ := p.call(t, "GET", "/v1/payments?limit="+bad, tokAda, adaLive, ""); resp.StatusCode != 400 {
			t.Errorf("limit %q: %d, want 400", bad, resp.StatusCode)
		}
	}
	down := newProduct(t, nil, nil) // no payments-worker configured
	resp, m := down.call(t, "GET", "/v1/payments", tokAda, adaLive, "")
	if resp.StatusCode != 503 {
		t.Errorf("without a payment service: %d %v, want 503", resp.StatusCode, m)
	}
	// Truncation is said out loud when the window is full.
	full := newProduct(t, func() []map[string]any {
		rows := make([]map[string]any, 0, ledgerWindowRows)
		for i := 0; i < ledgerWindowRows; i++ {
			rows = append(rows, map[string]any{"id": i + 1, "invoice_id": "i", "provider": "iyzico", "kind": "charge", "status": "succeeded", "amount_minor": 100,
				"currency": "USD", "provider_ref": "r" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)), "occurred_at": time.Now().Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)})
		}
		return rows
	}, nil)
	if _, m := full.call(t, "GET", "/v1/payments?limit=3", tokAda, adaLive, ""); m["truncated"] != true || len(m["payments"].([]any)) != 3 {
		t.Errorf("a full window is flagged: %v", m["truncated"])
	}
}

func TestOperatorOnlyBillingRoutesStayOutOfReachOfSessions(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/v1/tenants/" + adaLive + "/ledger"},
		{"GET", "/admin/v1/tenants/" + adaLive + "/invoices"},
		{"GET", "/admin/v1/tenants/" + adaLive + "/invoices/inv/pdf"},
		{"GET", "/admin/v1/tenants/" + adaLive + "/invoices/inv/saga"},
		{"GET", "/admin/v1/tenants/" + adaLive + "/reconciliation?period=2026-10"},
		{"GET", "/admin/v1/tenants/" + adaLive + "/provider-accounts"},
		{"GET", "/admin/v1/tenants/" + adaLive},
		{"GET", "/admin/v1/tenants"},
		{"POST", "/admin/v1/invoices/inv/payments"},
		{"POST", "/admin/v1/invoices/inv/credit-notes"},
	} {
		for _, tok := range []string{tokAda, tokViewer, tokBob} {
			for _, tn := range []string{"", adaLive} {
				if resp, _ := p.call(t, c.method, c.path, tok, tn, `{}`); resp.StatusCode != 403 {
					t.Errorf("%s %s with a session (tenant %q): %d, want 403 (a valid session that is not an operator's)", c.method, c.path, tn, resp.StatusCode)
				}
			}
		}
	}
}
