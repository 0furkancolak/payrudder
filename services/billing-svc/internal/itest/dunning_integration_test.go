//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	"github.com/0furkancolak/quotify/pkg/stubworker"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/api"
)

func testTenantDunningSettings(t *testing.T) {
	e := newEnv(t, envOpts{})
	svc := api.New(shared.app, shared.system, e.mgr, e.eng)
	tctx := tenant.NewContext(context.Background(), tenant.Info{ID: e.tid})

	got, err := svc.GetDunningSettings(tctx, &billingv1.GetDunningSettingsRequest{})
	if err != nil || !got.GetSettings().GetDefaultsInUse() || len(got.GetSettings().GetRetryHours()) != 3 {
		t.Fatalf("defaults = %+v, %v; want the three service-wide delays", got, err)
	}
	for _, bad := range [][]int32{{0}, {721}, {1, 1, 1, 1, 1, 1, 1}} {
		if _, err := svc.SetDunningSettings(tctx, &billingv1.SetDunningSettingsRequest{RetryHours: bad}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("retry_hours %v: %v, want InvalidArgument", bad, err)
		}
	}
	// One retry after 2 h, then give up.
	if _, err := svc.SetDunningSettings(tctx, &billingv1.SetDunningSettingsRequest{RetryHours: []int32{2}}); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.GetDunningSettings(tctx, &billingv1.GetDunningSettingsRequest{}); got.GetSettings().GetDefaultsInUse() || len(got.GetSettings().GetRetryHours()) != 1 {
		t.Fatalf("after set = %+v", got)
	}

	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "charge" {
			return stubworker.Action{Reply: stubworker.Declined}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	eventually(t, 20*time.Second, "first decline", func() bool { return e.sagaStates("2026-10")["cus_a"] == "PAYMENT_DECLINED" })
	e.clk.Advance(2*time.Hour + time.Minute)
	if res, err := e.eng.Sweep(context.Background(), shared.system, 100); err != nil || res.Dunned != 2 {
		t.Fatalf("dunning pass: %+v, %v; want 2 retries", res, err)
	}
	eventually(t, 30*time.Second, "uncollectible after the only retry", func() bool {
		var status string
		err := shared.system.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND snapshot->>'customer_external_id' = 'cus_a'`, e.tid).Scan(&status)
		return err == nil && status == "uncollectible"
	})
	if n := stub.Count("charge"); n != 4 { // 2 invoices × (first charge + one retry)
		t.Fatalf("charge commands = %d, want 4", n)
	}
}
