//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// seedDraft inserts a draft whose snapshot names the tenant's time zone (empty: a draft made before the zone was frozen).
func seedDraft(t *testing.T, e *env, zone string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	snap := `{"environment":"live","currency_exponent":2,"customer_external_id":"c","customer_email":"c@example.com"`
	if zone != "" {
		snap += `,"time_zone":"` + zone + `"`
	}
	snap += `}`
	if _, err := shared.sql.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot)
		VALUES ($1,$2,$3,'2026-10','REGULAR',$4,'USD',0,0,'draft',$5)`, e.tid, id, uuid.New(), uuid.New(), snap); err != nil {
		t.Fatal(err)
	}
	return id
}

// The year of a number is the year at the tenant's wall clock, and each year is a series of its own that stays gap-free whichever zone finalizes into it (docs/plans/05,
// billing-svc-status 19). Drafts made before the zone was frozen into the snapshot keep dating by UTC.
func testInvoiceNumberYearIsTheTenantsYear(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.clk.Advance(time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC).Sub(e.clk.Now())) // 01:30 on 1 January 2027 in Istanbul, 12:30 on the 31st in Honolulu
	finalize := func(zone string) string {
		t.Helper()
		id := seedDraft(t, e, zone)
		if err := e.inv.Finalize(context.Background(), e.tid, id); err != nil {
			t.Fatal(err)
		}
		var number string
		if err := shared.sql.QueryRow(context.Background(), `SELECT number FROM invoices WHERE tenant_id = $1 AND id = $2`, e.tid, id).Scan(&number); err != nil {
			t.Fatal(err)
		}
		return number
	}
	for i, want := range []struct{ zone, number string }{
		{"", "INV-2026-000001"},                // an older draft: UTC, as before
		{"Europe/Istanbul", "INV-2027-000001"}, // the new year has begun there
		{"Pacific/Honolulu", "INV-2026-000002"},
		{"Europe/Istanbul", "INV-2027-000002"},
		{"Not/AZone", "INV-2026-000003"}, // a zone that does not load is UTC
		{"Europe/Istanbul", "INV-2027-000003"},
	} {
		if got := finalize(want.zone); got != want.number {
			t.Fatalf("finalize #%d in %q = %s, want %s", i+1, want.zone, got, want.number)
		}
	}
}

// A credit note is numbered by the year of the invoice's zone as well.
func testCreditNoteNumberYearIsTheTenantsYear(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.clk.Advance(time.Date(2026, 12, 31, 22, 30, 0, 0, time.UTC).Sub(e.clk.Now()))
	svc := e.api()
	op := tenant.WithOperator(context.Background())
	create := func(zone string) string {
		t.Helper()
		id := uuid.New()
		snap := `{"customer_external_id":"cus_year","environment":"live"`
		if zone != "" {
			snap += `,"time_zone":"` + zone + `"`
		}
		if _, err := shared.sql.Exec(context.Background(), `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, number, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider)
			VALUES ($1,$2,$3,$4,'REGULAR',$5,$6,'USD',1000,1000,'paid',$7::jsonb,'')`, e.tid, id, uuid.New(), "2025-10", "INV-2026-"+id.String()[:6], uuid.New(), snap+"}"); err != nil {
			t.Fatal(err)
		}
		r, err := svc.CreateCreditNote(op, &billingv1.CreateCreditNoteRequest{TenantId: e.tid.String(), InvoiceId: id.String(), AmountMinor: 100})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetCreditNote().GetNumber()
	}
	for i, want := range []struct{ zone, number string }{
		{"", "CN-2026-000001"},
		{"Europe/Istanbul", "CN-2027-000001"},
		{"Pacific/Honolulu", "CN-2026-000002"},
		{"Europe/Istanbul", "CN-2027-000002"},
	} {
		if got := create(want.zone); got != want.number {
			t.Fatalf("credit note #%d in %q = %s, want %s", i+1, want.zone, got, want.number)
		}
	}
}
