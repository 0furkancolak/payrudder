//go:build integration

package itest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// Seats multiply the plan's fixed fee. A change of quantity is priced like a change of plan on the two sums: the unused time of the old seats is credited, the new
// seats are charged for the rest of the period, the preview is what is then made, and a coupon is taken off both.
func testSubscriptionQuantity(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Quantity")[plansv1.Environment_ENVIRONMENT_LIVE] // TRY
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "team", Name: "team"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "team", BaseFeeMinor: 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "HALFQ", PercentBps: 5000, Duration: plansv1.CouponDuration_COUPON_DURATION_FOREVER}}); err != nil {
		t.Fatal(err)
	}
	n := 0
	// subscribe creates an advance-billed subscription of seats seats that started ten days ago, and marks its first fee as invoiced, as billing-svc would.
	subscribe := func(seats int32, coupon string) *plansv1.Subscription {
		t.Helper()
		n++
		ext := "cus_qty_" + string(rune('a'+n))
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "team", StartTime: timestamppb.New(start), TrialDays: proto.Int32(0),
			Quantity: seats, CouponCode: coupon})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
			t.Fatal(err)
		}
		return r.GetSubscription()
	}
	seatsReq := func(id string, seats int32) *plansv1.ChangeSubscriptionRequest {
		return &plansv1.ChangeSubscriptionRequest{SubscriptionId: id, Quantity: seats}
	}
	preview := func(req *plansv1.ChangeSubscriptionRequest) (*plansv1.ChangeQuote, error) {
		r, err := e.plans.PreviewSubscriptionChange(ctx, &plansv1.PreviewSubscriptionChangeRequest{Change: req})
		return r.GetQuote(), err
	}
	// The credit is the unused share of the old seats' fee and the charge the same share of the new seats': their ratio is the ratio of the seats, up to rounding.
	ratio := func(t *testing.T, credit, charge int64, oldSeats, newSeats int64) {
		t.Helper()
		if d := charge*oldSeats - credit*newSeats; d < -2*(oldSeats+newSeats) || d > 2*(oldSeats+newSeats) {
			t.Fatalf("credit %d for %d seats and charge %d for %d seats are not shares of the same time", credit, oldSeats, charge, newSeats)
		}
	}

	t.Run("a new subscription's fee is the plan's times the seats, and the default is one seat", func(t *testing.T) {
		one := subscribe(0, "")
		five := subscribe(5, "")
		if one.GetQuantity() != 1 || five.GetQuantity() != 5 {
			t.Fatalf("quantities = %d and %d; want 1 and 5", one.GetQuantity(), five.GetQuantity())
		}
		due, err := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(time.Now().Add(60 * 24 * time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		fees := map[string]int64{}
		for _, d := range due.GetDue() {
			fees[d.GetSubscription().GetId()] = d.GetFeeMinor()
			if d.GetSubscription().GetId() == five.GetId() && d.GetSubscription().GetQuantity() != 5 {
				t.Errorf("fee due shows %d seats", d.GetSubscription().GetQuantity())
			}
		}
		if fees[one.GetId()] != 3000 || fees[five.GetId()] != 15000 {
			t.Fatalf("fees due = %v; want 3000 for one seat and 15000 for five", fees)
		}
		// The subscription report prices the seats too: MRR is the fee times the quantity, as invoiced.
		rep, err := e.plans.GetSubscriptionReport(ctx, &plansv1.GetSubscriptionReportRequest{GroupBy: []string{"plan"}})
		if err != nil {
			t.Fatal(err)
		}
		if rows := rep.GetRows(); len(rows) != 1 || rows[0].GetPlanCode() != "team" || rows[0].GetActive() != 2 || rows[0].GetMrrMinor() != 3000+5*3000 {
			t.Fatalf("report = %v; want 2 subscriptions and an MRR of 18000 (1 + 5 seats of 3000)", rep.GetRows())
		}
	})

	t.Run("more seats are charged for the rest of the period and the preview is what is made", func(t *testing.T) {
		sub := subscribe(5, "")
		q, err := preview(seatsReq(sub.GetId(), 8))
		if err != nil {
			t.Fatal(err)
		}
		if q.GetTiming() != plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY || q.GetProration() != plansv1.ChangeProration_CHANGE_PRORATION_PRORATE || q.GetQuantity() != 8 ||
			q.GetCreditMinor() <= 0 || q.GetCreditMinor() >= 15000 || q.GetNetMinor() != q.GetChargeMinor()-q.GetCreditMinor() || q.GetNetMinor() <= 0 || q.GetNextRenewalMinor() != 24000 {
			t.Fatalf("quote for 5 -> 8 seats = %v", q)
		}
		ratio(t, q.GetCreditMinor(), q.GetChargeMinor(), 5, 8)
		r, err := e.plans.ChangeSubscription(ctx, seatsReq(sub.GetId(), 8))
		if err != nil {
			t.Fatal(err)
		}
		nu := r.GetSubscription()
		if r.GetScheduled() || nu.GetId() == sub.GetId() || nu.GetQuantity() != 8 || nu.GetPlanCode() != "team" || nu.GetChangedFromId() != sub.GetId() {
			t.Fatalf("change = %v", r)
		}
		if d := r.GetQuote().GetNetMinor() - q.GetNetMinor(); d < -3 || d > 3 {
			t.Fatalf("preview net %d and change net %d disagree", q.GetNetMinor(), r.GetQuote().GetNetMinor())
		}
		adj, err := e.plans.ListSubscriptionAdjustments(ctx, &plansv1.ListSubscriptionAdjustmentsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range adj.GetAdjustments() {
			if a.GetSubscription().GetId() == nu.GetId() {
				found = a.GetCreditMinor() == r.GetQuote().GetCreditMinor() && a.GetChargeMinor() == r.GetQuote().GetChargeMinor()
			}
		}
		if !found {
			t.Error("no adjustment with the quoted credit and charge")
		}
		// The next period's fee is the new seats'.
		due, err := e.plans.ListSubscriptionsFeeDue(ctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(time.Now().Add(60 * 24 * time.Hour))})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range due.GetDue() {
			if d.GetSubscription().GetId() == nu.GetId() && d.GetFeeMinor() != 24000 {
				t.Errorf("next fee = %d; want 24000", d.GetFeeMinor())
			}
		}
	})

	t.Run("fewer seats credit the unused time of the removed ones, at once", func(t *testing.T) {
		sub := subscribe(5, "")
		r, err := e.plans.ChangeSubscription(ctx, seatsReq(sub.GetId(), 3))
		if err != nil {
			t.Fatal(err)
		}
		q := r.GetQuote()
		if r.GetScheduled() || r.GetSubscription().GetQuantity() != 3 || q.GetTiming() != plansv1.ChangeTiming_CHANGE_TIMING_IMMEDIATELY || q.GetNetMinor() >= 0 || q.GetNextRenewalMinor() != 9000 {
			t.Fatalf("5 -> 3 seats = %v", r)
		}
		ratio(t, q.GetCreditMinor(), q.GetChargeMinor(), 5, 3)
	})

	t.Run("fewer seats can wait for the end of the period; the waiting change carries the quantity", func(t *testing.T) {
		sub := subscribe(5, "")
		req := seatsReq(sub.GetId(), 2)
		req.Timing = plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END
		r, err := e.plans.ChangeSubscription(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if !r.GetScheduled() || r.GetSubscription().GetQuantity() != 5 || r.GetSubscription().GetScheduledChange().GetQuantity() != 2 || r.GetQuote().GetNetMinor() != 0 || r.GetQuote().GetNextRenewalMinor() != 6000 {
			t.Fatalf("scheduled = %v", r)
		}
		effective := r.GetQuote().GetEffectiveTime().AsTime()
		if _, err := e.plans.ApplyScheduledChanges(ctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(effective.Add(time.Second))}); err != nil {
			t.Fatal(err)
		}
		l, err := e.plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{CustomerExternalId: sub.GetCustomerExternalId()})
		if err != nil {
			t.Fatal(err)
		}
		if cur := l.GetSubscriptions()[0]; cur.GetQuantity() != 2 || cur.GetChangedFromId() != sub.GetId() {
			t.Fatalf("after the change: %v", cur)
		}
	})

	t.Run("a coupon is taken off both the credit and the charge", func(t *testing.T) {
		list := subscribe(5, "")
		half := subscribe(5, "HALFQ")
		lq, err := preview(seatsReq(list.GetId(), 8))
		if err != nil {
			t.Fatal(err)
		}
		hq, err := preview(seatsReq(half.GetId(), 8))
		if err != nil {
			t.Fatal(err)
		}
		near := func(got, want int64) bool { return got >= want-3 && got <= want+3 }
		if !near(hq.GetCreditMinor(), lq.GetCreditMinor()/2) || !near(hq.GetChargeMinor(), lq.GetChargeMinor()/2) || hq.GetNextRenewalMinor() != 24000 {
			t.Fatalf("quote with a 50 %% coupon = credit %d charge %d renewal %d; want half of %d and %d, and the list renewal 24000",
				hq.GetCreditMinor(), hq.GetChargeMinor(), hq.GetNextRenewalMinor(), lq.GetCreditMinor(), lq.GetChargeMinor())
		}
	})

	t.Run("refusals", func(t *testing.T) {
		sub := subscribe(5, "")
		if _, err := e.plans.ChangeSubscription(ctx, seatsReq(sub.GetId(), 5)); code(t, err) != codes.InvalidArgument {
			t.Fatalf("the quantity it has: %v", err)
		}
		for _, bad := range []int32{-1, 100001} {
			if _, err := e.plans.ChangeSubscription(ctx, seatsReq(sub.GetId(), bad)); code(t, err) != codes.InvalidArgument {
				t.Fatalf("quantity %d: %v", bad, err)
			}
		}
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_qty_arrears", Name: "a", Email: "a@example.com"}); err != nil {
			t.Fatal(err)
		}
		_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_qty_arrears", PlanCode: "team", FeeTiming: plansv1.FeeTiming_FEE_TIMING_ARREARS, Quantity: 3})
		if code(t, err) != codes.FailedPrecondition || errkey.Of(err) != "plans.subscription.quantity_needs_advance" {
			t.Fatalf("seats on an arrears subscription: %v (key %q)", err, errkey.Of(err))
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_qty_arrears", PlanCode: "team", Quantity: -2}); code(t, err) != codes.InvalidArgument {
			t.Fatalf("negative quantity on create: %v", err)
		}
	})
}
