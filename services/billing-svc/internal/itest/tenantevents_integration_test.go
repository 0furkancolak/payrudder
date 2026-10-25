//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
)

func testTenantEvents(t *testing.T) {
	e := newEnv(t, envOpts{})
	// The hand-over works across tenants: what earlier tests left behind must not count here.
	if _, err := shared.system.Exec(context.Background(), `DELETE FROM tenant_event_outbox`); err != nil {
		t.Fatal(err)
	}
	e.startWorkers(nil)
	completeOctober(e)

	counts := func() map[string]int {
		t.Helper()
		rows, err := shared.system.Query(context.Background(), `SELECT type, count(*) FROM tenant_event_outbox WHERE tenant_id = $1 GROUP BY type`, e.tid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]int{}
		for rows.Next() {
			var typ string
			var n int
			if err := rows.Scan(&typ, &n); err != nil {
				t.Fatal(err)
			}
			out[typ] = n
		}
		return out
	}
	// Two customers were invoiced and paid: each invoice announced twice, once.
	if c := counts(); c["invoice.finalized"] != 2 || c["invoice.paid"] != 2 || len(c) != 2 {
		t.Fatalf("outbox = %v, want two finalized and two paid invoices", c)
	}

	// A credit note is announced like an invoice.
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	var inv string
	if err := shared.system.QueryRow(context.Background(), `SELECT id::text FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_a' AND kind = 'REGULAR'`, e.tid).Scan(&inv); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateCreditNote(tenant.WithOperator(context.Background()), &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: inv, AmountMinor: 100, Reason: "goodwill"}); err != nil {
		t.Fatal(err)
	}
	if c := counts(); c["credit_note.created"] != 1 {
		t.Fatalf("outbox = %v, want a credit note event", c)
	}

	// plans-svc being down leaves everything in place; once it is back each event is handed over once and removed.
	e.events.failWith(status.Error(codes.Unavailable, "plans-svc is down"))
	if err := e.mgr.FlushTenantEvents(context.Background()); err == nil {
		t.Fatal("a failed hand-over must be reported")
	}
	if len(e.events.recorded(e.tid)) != 0 {
		t.Fatal("nothing should be recorded while plans-svc is down")
	}
	e.events.failWith(nil)
	if err := e.mgr.FlushTenantEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.events.recorded(e.tid)
	if len(got) != 5 {
		t.Fatalf("handed over %d events, want 5", len(got))
	}
	ids := map[string]bool{}
	for _, r := range got {
		if ids[r.GetEventId()] {
			t.Errorf("event %s handed over twice", r.GetEventId())
		}
		ids[r.GetEventId()] = true
		var data map[string]any
		if err := json.Unmarshal([]byte(r.GetDataJson()), &data); err != nil {
			t.Fatalf("%s: data is not JSON: %v", r.GetType(), err)
		}
		if r.GetType() == "invoice.paid" && (data["status"] != "paid" || data["customer_external_id"] == "" || data["number"] == "") {
			t.Errorf("invoice.paid data = %v", data)
		}
	}
	if c := counts(); len(c) != 0 {
		t.Fatalf("outbox after the hand-over = %v, want empty", c)
	}
}
