//go:build integration

package itest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	webhooksworker "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/worker"
)

// A price change moves the subscriptions of one plan version to another at their next renewal, only when that renewal is far enough away to tell the customer, and
// names the ones it left alone. It is a record of its own: running it again continues it, the skipped subscriptions can be tried again, and what is still waiting can
// be withdrawn in one call.
func testPriceChange(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Price change")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	for _, fee := range []int64{3000, 4000} {
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: fee}); err != nil {
			t.Fatal(err)
		}
	}
	subscribe := func(ext string, startedDaysAgo int, timing plansv1.FeeTiming) string {
		t.Helper()
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: ext, Name: ext, Email: ext + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		start := time.Now().Add(-time.Duration(startedDaysAgo) * 24 * time.Hour).Truncate(time.Microsecond)
		r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", PlanVersion: 1, StartTime: timestamppb.New(start), FeeTiming: timing})
		if err != nil {
			t.Fatal(err)
		}
		if timing != plansv1.FeeTiming_FEE_TIMING_ARREARS {
			if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
				t.Fatal(err)
			}
		}
		return r.GetSubscription().GetId()
	}
	// far renews in about 25 days and held, in about 23: both are far enough away, but held is a customer the seller blocked. near renews in 5 days, and arrears is
	// billed afterwards (a price change cannot move it).
	far := subscribe("cus_far", 5, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
	near := subscribe("cus_near", 25, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
	arrears := subscribe("cus_arrears", 5, plansv1.FeeTiming_FEE_TIMING_ARREARS)
	held := subscribe("cus_held", 7, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
	if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_held", Actor: "boss@example.com"}); err != nil {
		t.Fatal(err)
	}
	reasons := func(r *plansv1.SchedulePriceChangeResponse) map[string]string {
		m := map[string]string{}
		for _, s := range r.GetSkipped() {
			m[s.GetSubscriptionId()] = s.GetReason()
		}
		return m
	}
	run := func(notice int32, dry bool) *plansv1.SchedulePriceChangeResponse {
		t.Helper()
		r, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: notice, DryRun: dry, Actor: "boss@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	facts := func() int {
		t.Helper()
		var n int
		// The tenant's outbox is where the tenant is: the pooled table, or its schema in cells mode.
		if err := e.scan(ctx, live.GetId(), `SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = 'subscription.price-change.v1'`, []any{uuid.MustParse(live.GetId())}, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	changes := func() []*plansv1.PriceChange {
		t.Helper()
		l, err := e.plans.ListPriceChanges(ctx, &plansv1.ListPriceChangesRequest{PlanCode: "basic"})
		if err != nil {
			t.Fatal(err)
		}
		return l.GetPriceChanges()
	}
	items := func(id, state string) map[string]*plansv1.PriceChangeItem {
		t.Helper()
		g, err := e.plans.GetPriceChange(ctx, &plansv1.GetPriceChangeRequest{Id: id, State: state})
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]*plansv1.PriceChangeItem{}
		for _, it := range g.GetItems() {
			m[it.GetSubscriptionId()] = it
		}
		return m
	}
	rcv := newReceiver(t)
	if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: rcv.URL, EventTypes: []string{"price_change.scheduled"}}); err != nil {
		t.Fatal(err)
	}
	e.clearDeliveries(t)

	t.Run("notice days are bounded and the versions must differ", func(t *testing.T) {
		_, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, NoticeDays: 7})
		wantCode(t, "7 days of notice", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 2, ToVersion: 2})
		wantCode(t, "same version", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "nope", FromVersion: 1})
		wantCode(t, "unknown plan", err, codes.NotFound, "")
	})

	t.Run("a dry run counts and changes nothing, not even a record", func(t *testing.T) {
		r := run(14, true)
		if r.GetScheduled() != 1 || r.GetScheduledSubscriptionIds()[0] != far || r.GetPriceChangeId() != "" {
			t.Fatalf("dry run: %v", r)
		}
		if again := run(14, true); again.GetScheduled() != 1 || len(changes()) != 0 || facts() != 0 {
			t.Fatalf("a dry run changed something: %v, %d price changes, %d facts", again, len(changes()), facts())
		}
	})

	var id string
	t.Run("only the renewals far enough away are scheduled; the others are named with why; the run is recorded", func(t *testing.T) {
		r := run(14, false)
		if r.GetScheduled() != 1 || r.GetToVersion() != 2 || r.GetNoticeDays() != 14 || r.GetPriceChangeId() == "" || r.GetAlreadyScheduled() != 0 {
			t.Fatalf("response: %v", r)
		}
		id = r.GetPriceChangeId()
		// The customer's mail is one fact in the outbox, for the one scheduled change.
		if n := facts(); n != 1 {
			t.Errorf("price-change facts in the outbox: %d", n)
		}
		got := reasons(r)
		if got[near] != "renews_soon" || got[arrears] != "not_changeable" || got[held] != "customer_blocked" || len(got) != 3 {
			t.Errorf("skipped: %v", got)
		}
		l := changes()
		if len(l) != 1 || l[0].GetId() != id || l[0].GetState() != "open" || l[0].GetFromVersion() != 1 || l[0].GetToVersion() != 2 || l[0].GetNoticeDays() != 14 ||
			l[0].GetCreatedBy() != "boss@example.com" || l[0].GetScheduled() != 1 || l[0].GetSkipped() != 3 || l[0].GetApplied() != 0 || l[0].GetWithdrawn() != 0 {
			t.Fatalf("price changes: %v", l)
		}
		all := items(id, "")
		f := all[far]
		if len(all) != 4 || f.GetState() != "scheduled" || f.GetNotifyTime() == nil || f.GetEffectiveTime() == nil || f.GetReason() != "" {
			t.Fatalf("items: %v", all)
		}
		if f.GetEffectiveTime().AsTime().Before(time.Now().Add(14 * 24 * time.Hour)) {
			t.Errorf("the renewal %v is less than the notice away", f.GetEffectiveTime().AsTime())
		}
		// The record's date is the first renewal a price changes on, not the end of the notice.
		if !l[0].GetEffectiveTime().AsTime().Equal(f.GetEffectiveTime().AsTime()) {
			t.Errorf("effective_time %v, want the renewal %v", l[0].GetEffectiveTime().AsTime(), f.GetEffectiveTime().AsTime())
		}
		if sk := items(id, "skipped"); len(sk) != 3 || sk[near].GetReason() != "renews_soon" || sk[near].GetNotifyTime() != nil || sk[held].GetReason() != "customer_blocked" {
			t.Errorf("skipped items: %v", sk)
		}
		_, err := e.plans.GetPriceChange(ctx, &plansv1.GetPriceChangeRequest{Id: id, State: "done"})
		wantCode(t, "unknown state", err, codes.InvalidArgument, "plans.request.invalid")
	})

	t.Run("running it again continues the price change: what is scheduled is neither scheduled nor mailed twice", func(t *testing.T) {
		r := run(14, false)
		if r.GetScheduled() != 0 || r.GetAlreadyScheduled() != 1 || r.GetPriceChangeId() != id {
			t.Fatalf("second run: %v", r)
		}
		if got := reasons(r); got[far] != "" || len(got) != 3 {
			t.Errorf("second run skipped: %v", got)
		}
		if facts() != 1 || len(changes()) != 1 {
			t.Errorf("a second run made %d facts and %d price changes", facts(), len(changes()))
		}
		// A dry run now sees it too.
		if d := run(14, true); d.GetScheduled() != 0 || d.GetAlreadyScheduled() != 1 || d.GetPriceChangeId() != id {
			t.Errorf("dry run after: %v", d)
		}
	})

	t.Run("the seller's webhooks hear of the run that scheduled something, once", func(t *testing.T) {
		now := time.Now().Add(time.Minute)
		disp := &webhooksworker.Dispatcher{Pool: e.system, Places: e.places, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return now }}
		if n, err := disp.Once(ctx); err != nil || n != 1 {
			t.Fatalf("dispatcher claimed %d, %v; want the one run that scheduled something", n, err)
		}
		calls := rcv.calls()
		if len(calls) != 1 {
			t.Fatalf("%d deliveries", len(calls))
		}
		var env struct {
			Type string `json:"type"`
			Data struct {
				PriceChange map[string]any `json:"price_change"`
				Scheduled   int            `json:"scheduled"`
				Skipped     int            `json:"skipped"`
			} `json:"data"`
		}
		if err := json.Unmarshal(calls[0].body, &env); err != nil {
			t.Fatal(err)
		}
		pc := env.Data.PriceChange
		if env.Type != "price_change.scheduled" || env.Data.Scheduled != 1 || env.Data.Skipped != 3 || pc["id"] != id || pc["plan_code"] != "basic" || pc["state"] != "open" ||
			pc["from_version"] != float64(1) || pc["to_version"] != float64(2) || pc["notice_days"] != float64(14) || pc["scheduled"] != float64(1) || pc["effective_time"] == nil {
			t.Errorf("event = %s", calls[0].body)
		}
	})

	t.Run("a retry tries the skipped again, and picks up a subscription that joined since", func(t *testing.T) {
		if _, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_held", Actor: "boss@example.com"}); err != nil {
			t.Fatal(err)
		}
		late := subscribe("cus_late", 1, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
		retried, err := e.plans.RetryPriceChange(ctx, &plansv1.RetryPriceChangeRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		r := retried.GetResult()
		if r.GetScheduled() != 2 || r.GetAlreadyScheduled() != 1 || r.GetPriceChangeId() != id || r.GetNoticeDays() != 14 {
			t.Fatalf("retry: %v", r)
		}
		if got := reasons(r); got[near] != "renews_soon" || got[arrears] != "not_changeable" || len(got) != 2 {
			t.Errorf("retry skipped: %v", got)
		}
		if facts() != 3 {
			t.Errorf("facts after the retry: %d, want one per scheduled subscription", facts())
		}
		l := changes()
		if len(l) != 1 || l[0].GetScheduled() != 3 || l[0].GetSkipped() != 2 {
			t.Fatalf("after the retry: %v", l)
		}
		if sch := items(id, "scheduled"); len(sch) != 3 || sch[held] == nil || sch[late] == nil || sch[far] == nil {
			t.Errorf("scheduled items: %v", sch)
		}
		_, err = e.plans.RetryPriceChange(ctx, &plansv1.RetryPriceChangeRequest{Id: "0192b7a0-0000-7000-8000-000000000000"})
		wantCode(t, "unknown price change", err, codes.NotFound, "plans.price_change.not_found")
	})

	t.Run("a withdrawal cancels what is waiting, keeps what was applied, and closes the price change", func(t *testing.T) {
		// Apply the changes of the subscriptions renewing first (far and held); the one that joined last (late) is still waiting.
		first := items(id, "scheduled")[far].GetEffectiveTime().AsTime()
		if heldAt := items(id, "scheduled")[held].GetEffectiveTime().AsTime(); heldAt.After(first) {
			first = heldAt
		}
		if a, err := e.plans.ApplyScheduledChanges(ctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(first.Add(time.Hour))}); err != nil || a.GetApplied() != 2 {
			t.Fatalf("applied %v, %v", a, err)
		}
		w, err := e.plans.WithdrawPriceChange(ctx, &plansv1.WithdrawPriceChangeRequest{Id: id, Actor: "boss@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		pc := w.GetPriceChange()
		if w.GetWithdrawn() != 1 || pc.GetState() != "withdrawn" || pc.GetWithdrawnBy() != "boss@example.com" || pc.GetWithdrawTime() == nil ||
			pc.GetScheduled() != 0 || pc.GetApplied() != 2 || pc.GetWithdrawn() != 1 || pc.GetSkipped() != 2 {
			t.Fatalf("withdraw: %v", w)
		}
		if w2, err := e.plans.WithdrawPriceChange(ctx, &plansv1.WithdrawPriceChangeRequest{Id: id}); err != nil || w2.GetWithdrawn() != 0 || w2.GetPriceChange().GetWithdrawnBy() != "boss@example.com" {
			t.Errorf("a second withdrawal: %v %v", w2, err)
		}
		_, err = e.plans.RetryPriceChange(ctx, &plansv1.RetryPriceChangeRequest{Id: id})
		wantCode(t, "retry after the withdrawal", err, codes.FailedPrecondition, "plans.price_change.withdrawn")
		_, err = e.plans.WithdrawPriceChange(ctx, &plansv1.WithdrawPriceChangeRequest{Id: "not-an-id"})
		wantCode(t, "withdraw a bad id", err, codes.NotFound, "plans.price_change.not_found")
	})

	t.Run("a new run after a withdrawal makes a new price change; a change the seller withdrew by itself stays withdrawn; another change waiting is left alone", func(t *testing.T) {
		manual := subscribe("cus_manual", 1, plansv1.FeeTiming_FEE_TIMING_ADVANCE)
		if _, err := e.plans.ChangeSubscription(ctx, &plansv1.ChangeSubscriptionRequest{SubscriptionId: manual, PlanCode: "basic", PlanVersion: 2, Timing: plansv1.ChangeTiming_CHANGE_TIMING_PERIOD_END}); err != nil {
			t.Fatal(err)
		}
		r := run(14, false)
		if r.GetPriceChangeId() == "" || r.GetPriceChangeId() == id || r.GetScheduled() != 1 {
			t.Fatalf("run after the withdrawal: %v", r)
		}
		if got := reasons(r); got[manual] != "has_pending_change" || got[near] != "renews_soon" {
			t.Errorf("skipped: %v", got)
		}
		if len(changes()) != 2 {
			t.Fatalf("price changes: %v", changes())
		}
		// The seller takes one customer's change back by itself; a retry does not put it back.
		sch := items(r.GetPriceChangeId(), "scheduled")
		if len(sch) != 1 {
			t.Fatalf("scheduled items: %v", sch)
		}
		var one string
		for sub := range sch {
			one = sub
		}
		if _, err := e.plans.CancelScheduledChange(ctx, &plansv1.CancelScheduledChangeRequest{SubscriptionId: one}); err != nil {
			t.Fatal(err)
		}
		retried, err := e.plans.RetryPriceChange(ctx, &plansv1.RetryPriceChangeRequest{Id: r.GetPriceChangeId()})
		again := retried.GetResult()
		if err != nil || again.GetScheduled() != 0 || again.GetAlreadyScheduled() != 0 || reasons(again)[one] != "" {
			t.Fatalf("retry after the seller's own withdrawal: %v %v", again, err)
		}
		if w := items(r.GetPriceChangeId(), "withdrawn"); len(w) != 1 || w[one] == nil {
			t.Errorf("withdrawn items: %v", w)
		}
	})
}
