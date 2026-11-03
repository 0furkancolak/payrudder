//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	changescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/command"
)

// Plan 15: the notice mail of a price change can be held until N days before the new price (notice_send_days_before). The run schedules the change at once but keeps the
// fact in the item; the sweep writes it to the outbox when it is due, once, never for a withdrawn change, and not while the operator's kill switch is on.
func testPriceChangeNoticeHold(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Price notice hold")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	tid := uuid.MustParse(live.GetId())
	day := 24 * time.Hour
	for _, code := range []string{"basic", "pro"} {
		if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: code, Name: code}); err != nil {
			t.Fatal(err)
		}
		for _, fee := range []int64{3000, 4000} {
			if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: code, BaseFeeMinor: fee}); err != nil {
				t.Fatal(err)
			}
		}
	}
	subscribe := func(ext, plan string) string {
		t.Helper()
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-5 * day).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: plan, PlanVersion: 1, StartTime: timestamppb.New(start)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		return r.GetSubscription().GetId()
	}
	basic, pro := subscribe("cus_basic", "basic"), subscribe("cus_pro", "pro")
	run := func(plan string, req *plansv1.SchedulePriceChangeRequest) (*plansv1.SchedulePriceChangeResponse, error) {
		req.PlanCode, req.FromVersion = plan, 1
		return e.plans.SchedulePriceChange(ctx, req)
	}
	facts := func(sub string) int {
		t.Helper()
		var n int
		if err := e.scan(context.Background(), tid.String(), `SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = 'subscription.price-change.v1' AND position(convert_to($2, 'UTF8') in payload) > 0`, []any{tid, sub}, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var clock atomic.Int64
	at := func(d time.Duration) { clock.Store(time.Now().Add(d).UnixNano()) }
	sweep := changescmd.NewNoticeSweeper(e.system, e.appDB, e.places, 0, slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(func() time.Time { return time.Unix(0, clock.Load()) })
	// The sweep reads across tenants: what a test asserts is its own tenant's rows.
	once := func() {
		t.Helper()
		if _, _, err := sweep.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	pause := func(paused bool) {
		t.Helper()
		if _, err := e.admin.SetPriceChangePause(operator(), &plansv1.SetPriceChangePauseRequest{Paused: paused, Reason: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	defer pause(false)

	from := time.Now().Add(100 * day).Truncate(time.Microsecond)
	t.Run("the value is checked: 0, or between the notice and 365", func(t *testing.T) {
		for _, d := range []int32{29, 366, -1} {
			_, err := run("basic", &plansv1.SchedulePriceChangeRequest{NoticeDays: 30, DryRun: true, NoticeSendDaysBefore: d})
			wantCode(t, "send days", err, codes.InvalidArgument, "plans.request.invalid")
		}
	})

	// Monthly, started 5 days ago: the first renewal on or after day 100 is about day 117, so the mail is due about day 77 (40 days before).
	r, err := run("basic", &plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from), NoticeSendDaysBefore: 40})
	if err != nil || r.GetScheduled() != 1 {
		t.Fatalf("run: %v, %v", r, err)
	}
	rp, err := run("pro", &plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from), NoticeSendDaysBefore: 40})
	if err != nil || rp.GetScheduled() != 1 {
		t.Fatalf("run pro: %v, %v", rp, err)
	}
	t.Run("the run holds the mail: nothing is queued and the item is not notified", func(t *testing.T) {
		if facts(basic) != 0 || facts(pro) != 0 {
			t.Fatalf("facts %d and %d at the run", facts(basic), facts(pro))
		}
		g, err := e.plans.GetPriceChange(ctx, &plansv1.GetPriceChangeRequest{Id: r.GetPriceChangeId()})
		if err != nil || g.GetPriceChange().GetNoticeSendDaysBefore() != 40 || g.GetItems()[0].GetNotifyTime() != nil {
			t.Fatalf("record: %v, %v", g, err)
		}
	})
	t.Run("running it again must name the same value", func(t *testing.T) {
		_, err := run("basic", &plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from), NoticeSendDaysBefore: 50})
		wantCode(t, "another value", err, codes.FailedPrecondition, "plans.price_change.date_differs")
		_, err = run("basic", &plansv1.SchedulePriceChangeRequest{NoticeDays: 30, EffectiveFrom: timestamppb.New(from)})
		wantCode(t, "none", err, codes.FailedPrecondition, "plans.price_change.date_differs")
	})
	t.Run("before it is due nothing is sent", func(t *testing.T) {
		at(10 * day)
		once()
		at(60 * day)
		once()
		if facts(basic) != 0 {
			t.Fatalf("facts = %d", facts(basic))
		}
	})
	t.Run("a withdrawn price change never sends its held notice", func(t *testing.T) {
		if _, err := e.plans.WithdrawPriceChange(ctx, &plansv1.WithdrawPriceChangeRequest{Id: rp.GetPriceChangeId()}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("while the kill switch is on a due notice is held, and goes when it is released", func(t *testing.T) {
		at(90 * day)
		pause(true)
		once()
		if facts(basic) != 0 {
			t.Fatalf("sent while paused: %d", facts(basic))
		}
		pause(false)
		once()
		if facts(basic) != 1 {
			t.Fatalf("after the release: %d", facts(basic))
		}
	})
	t.Run("it is sent once: a later pass and a second sweep send nothing more, the item is notified", func(t *testing.T) {
		at(91 * day)
		once()
		other := changescmd.NewNoticeSweeper(e.system, e.appDB, e.places, 0, slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(func() time.Time { return time.Unix(0, clock.Load()) })
		if _, _, err := other.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
		if facts(basic) != 1 || facts(pro) != 0 {
			t.Fatalf("facts %d and %d (withdrawn)", facts(basic), facts(pro))
		}
		g, err := e.plans.GetPriceChange(ctx, &plansv1.GetPriceChangeRequest{Id: r.GetPriceChangeId()})
		if err != nil || g.GetItems()[0].GetNotifyTime() == nil {
			t.Fatalf("item after the send: %v, %v", g, err)
		}
	})
}
