//go:build integration

package itest

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// What earlier invoices billed late for an origin period is added in the same unit, even when the meter's scale changed between them: 5 whole units and 2.000 units
// (stored as 2000 at scale 3) are 7 units, not 2.005.
func testPriorLateUsageAddsQuantitiesOfDifferentScales(t *testing.T) {
	e := newEnv(t, envOpts{})
	customer := uuid.New()
	for i, l := range []struct {
		period, quantity string
		scale            int
	}{{"2026-10", "5", 0}, {"2026-11", "2000", 3}} {
		inv := seedOne(t, e.tid, customer, "cus_scale", "open", "USD", "INV-2026-00092"+string(rune('1'+i)), 100, "live")
		if _, err := shared.system.Exec(context.Background(), `UPDATE invoices SET period_id = $3 WHERE tenant_id = $1 AND id = $2`, e.tid, inv, l.period); err != nil {
			t.Fatal(err)
		}
		if _, err := shared.system.Exec(context.Background(), `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, meter_code, origin_period_id, quantity, quantity_scale, amount_minor, description)
			VALUES ($1,$2,1,'LATE_USAGE','calls','2026-09',$3::numeric,$4,50,'late')`, e.tid, inv, l.quantity, l.scale); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := sqlc.New(shared.system).PriorLateUsage(context.Background(), sqlc.PriorLateUsageParams{TenantID: e.tid, Origins: []string{"2026-09"}, PeriodID: "2026-12"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("PriorLateUsage: %v, %v", rows, err)
	}
	got, _ := new(big.Rat).SetString(rows[0].Quantity)
	if got == nil || got.Cmp(big.NewRat(7, 1)) != 0 {
		t.Fatalf("prior late usage = %s; want 7", got)
	}
}
