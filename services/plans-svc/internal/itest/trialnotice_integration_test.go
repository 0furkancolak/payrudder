//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	trialcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/trialnotices/command"
	trialmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/trialnotices/model"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// testTrialReminders: a trial that ends within the window is reminded once (webhook event and the fact for the notifier, in one transaction), whatever the number of
// passes and instances; a trial that is extended is reminded again; trials that will not end in a charge, or that a provider runs, are not reminded.
func testTrialReminders(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	live := e.account(t, "Trial reminders")[plansv1.Environment_ENVIRONMENT_LIVE]
	other := e.account(t, "Trial reminders other")[plansv1.Environment_ENVIRONMENT_LIVE]
	tid := uuid.MustParse(live.GetId())
	tctx := as(live.GetId())

	for _, c := range []context.Context{tctx, as(other.GetId())} {
		if _, err := e.plans.CreatePlan(c, &plansv1.CreatePlanRequest{Code: "trial", Name: "Trial"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.PublishPlanVersion(c, &plansv1.PublishPlanVersionRequest{PlanCode: "trial", BaseFeeMinor: 5000, Fees: yearlyFees("trial", 5000)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.hooks.CreateWebhookEndpoint(tctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/hook", EventTypes: []string{webhooksmodel.SubscriptionTrialWillEnd}}); err != nil {
		t.Fatal(err)
	}
	subscribe := func(tenantCtx context.Context, customer string, days int32) *plansv1.Subscription {
		t.Helper()
		if _, err := e.plans.CreateCustomer(tenantCtx, &plansv1.CreateCustomerRequest{ExternalId: customer, Name: customer, Email: customer + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		r, err := e.plans.CreateSubscription(tenantCtx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: customer, PlanCode: "trial", TrialDays: proto.Int32(days)})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetSubscription()
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		// The first argument of every query here is the tenant: its rows are where the tenant is.
		if err := e.scan(ctx, args[0].(uuid.UUID).String(), query, args, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	events := func(tenant uuid.UUID) int {
		return count(`SELECT count(*) FROM webhook_events WHERE tenant_id = $1 AND type = $2`, tenant, webhooksmodel.SubscriptionTrialWillEnd)
	}
	facts := func(tenant uuid.UUID) int {
		return count(`SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = $2`, tenant, trialmodel.Topic)
	}
	notices := func(tenant uuid.UUID) int {
		return count(`SELECT count(*) FROM subscription_trial_notices WHERE tenant_id = $1`, tenant)
	}
	var clock atomic.Int64
	at := func(d time.Duration) { clock.Store(time.Now().Add(d).UnixNano()) }
	job := trialcmd.New(e.system, e.appDB, trialcmd.Config{Days: 3}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithPlaces(e.places).
		WithClock(func() time.Time { return time.Unix(0, clock.Load()) })
	// The job reads across tenants, so it also reminds the trials of the other tests that share this database: what a test asserts is its own tenants' rows.
	once := func() {
		t.Helper()
		if _, _, err := job.Once(ctx); err != nil {
			t.Fatal(err)
		}
	}
	const day = 24 * time.Hour

	sub := subscribe(tctx, "cus_ten", 10)
	end := sub.GetTrialEndTime().AsTime()
	subscribe(as(other.GetId()), "cus_other", 10)
	// The other tenant has no endpoint: its trial is still reminded (the mail), and its notice stays in its own rows.
	oid := uuid.MustParse(other.GetId())

	t.Run("a trial outside the window is not reminded", func(t *testing.T) {
		at(2 * day) // 8 days left
		once()
		if notices(tid) != 0 || notices(oid) != 0 {
			t.Fatalf("notices %d and %d", notices(tid), notices(oid))
		}
	})

	t.Run("inside the window it is reminded once: the event, the fact and the notice together", func(t *testing.T) {
		at(8 * day) // 2 days left
		once()
		if events(tid) != 1 || facts(tid) != 1 || notices(tid) != 1 {
			t.Fatalf("events %d, facts %d, notices %d", events(tid), facts(tid), notices(tid))
		}
		if events(oid) != 0 || facts(oid) != 1 || notices(oid) != 1 {
			t.Fatalf("other tenant: events %d (no endpoint), facts %d, notices %d", events(oid), facts(oid), notices(oid))
		}
		var payload []byte
		if err := e.scan(ctx, tid.String(), `SELECT payload FROM outbox WHERE tenant_id = $1 AND topic = $2`, []any{tid, trialmodel.Topic}, &payload); err != nil {
			t.Fatal(err)
		}
		var fact eventsv1.TrialWillEnd
		if err := proto.Unmarshal(payload, &fact); err != nil {
			t.Fatal(err)
		}
		if fact.GetTenantId() != live.GetId() || fact.GetSubscriptionId() != sub.GetId() || fact.GetPlanCode() != "trial" || !fact.GetTrialEndsAt().AsTime().Equal(end) ||
			fact.GetEventId() != trialmodel.EventID(tid, uuid.MustParse(sub.GetCustomerId()), end).String() {
			t.Fatalf("fact = %v", &fact)
		}
		var data string
		if err := e.scan(ctx, tid.String(), `SELECT data::text FROM webhook_events WHERE tenant_id = $1 AND type = $2`, []any{tid, webhooksmodel.SubscriptionTrialWillEnd}, &data); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(data, sub.GetId()) || !strings.Contains(data, `"SUBSCRIPTION_STATE_TRIALING"`) {
			t.Fatalf("event data does not carry the subscription: %s", data)
		}
		if count(`SELECT count(*) FROM webhook_deliveries WHERE tenant_id = $1 AND event_id IN (SELECT id FROM webhook_events WHERE tenant_id = $1 AND type = $2)`, tid, webhooksmodel.SubscriptionTrialWillEnd) != 1 {
			t.Fatal("the event was not queued for the endpoint")
		}
	})

	t.Run("a later pass reminds nothing", func(t *testing.T) {
		at(8*day + time.Hour)
		once()
		if events(tid) != 1 || facts(tid) != 1 || notices(tid) != 1 {
			t.Fatalf("a second pass reminded again: events %d, facts %d, notices %d", events(tid), facts(tid), notices(tid))
		}
	})

	t.Run("a trial that ended before the job came is not reminded", func(t *testing.T) {
		// A one-day trial is over at the clock of the previous steps (8 days on).
		subscribe(tctx, "cus_late", 1)
		once()
		if notices(tid) != 1 {
			t.Fatalf("a trial that was over was reminded: %d notices", notices(tid))
		}
	})

	t.Run("passes and instances at once remind one trial once", func(t *testing.T) {
		subscribe(tctx, "cus_race", 10)
		at(8*day + time.Hour)
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				other := trialcmd.New(e.system, e.appDB, trialcmd.Config{Days: 3}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithPlaces(e.places).WithClock(func() time.Time { return time.Unix(0, clock.Load()) })
				if _, _, err := other.Once(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		// cus_ten's and cus_other's were done before; only the new customer's remains.
		if events(tid) != 2 || facts(tid) != 2 || notices(tid) != 2 {
			t.Fatalf("events %d, facts %d, notices %d; want one of each more", events(tid), facts(tid), notices(tid))
		}
	})

	t.Run("an extended trial is reminded again, a plan change that keeps the end is not", func(t *testing.T) {
		before := events(tid)
		extra := 5 * day
		if err := pgxx.InTenantTx(ctx, e.appDB, tid, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE subscriptions SET trial_ends_at = trial_ends_at + $2::interval WHERE id = $1`, sub.GetId(), extra.String())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		at(8*day + time.Hour) // now 7 days before the new end: outside the window
		once()
		if events(tid) != before {
			t.Fatalf("reminded at 7 days: %d events, want %d", events(tid), before)
		}
		at(13*day + time.Hour) // 2 days before the new end
		once()
		if events(tid) != before+1 || facts(tid) != before+1 || notices(tid) != before+1 {
			t.Fatalf("events %d, facts %d, notices %d, want %d of each", events(tid), facts(tid), notices(tid), before+1)
		}
		once()
		if events(tid) != before+1 {
			t.Fatalf("a repeat pass reminded again: %d events", events(tid))
		}
		// Another subscription row of the same customer with the same trial end (what a plan change makes) shares the notice.
		if err := pgxx.InTenantTx(ctx, e.appDB, tid, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE subscriptions SET active = tstzrange(lower(active), now() + interval '1 second', '[)'), state = 'cancelled' WHERE id = $1`, sub.GetId())
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO subscriptions (tenant_id, id, customer_id, plan_version_id, active, state, interval, currency, trial_ends_at, billing_anchor, changed_from_id, owner, fee_timing)
				SELECT tenant_id, gen_random_uuid(), customer_id, plan_version_id, tstzrange(now() + interval '1 second', NULL, '[)'), 'trialing', interval, currency, trial_ends_at, billing_anchor, id, owner, fee_timing
				FROM subscriptions WHERE id = $1`, sub.GetId())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		once()
		if events(tid) != before+1 || notices(tid) != before+1 {
			t.Fatalf("the successor on the same trial end was reminded: %d events, %d notices", events(tid), notices(tid))
		}
	})

	t.Run("trials that will not convert, or that a provider runs, are not reminded", func(t *testing.T) {
		mk := func(name string, set string) {
			t.Helper()
			s := subscribe(tctx, name, 10)
			if err := pgxx.InTenantTx(ctx, e.appDB, tid, func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, `UPDATE subscriptions SET `+set+` WHERE id = $1`, s.GetId())
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		mk("cus_cancel", `cancel_at_period_end = true`)
		mk("cus_paused", `state = 'paused', paused_at = now()`)
		mk("cus_provider", `owner = 'provider', provider = 'iyzico', provider_ref = 'sub_ext'`)
		mk("cus_scheduled_end", `active = tstzrange(lower(active), now() + interval '9 days', '[)')`)
		at(8*day + time.Hour)
		before := notices(tid)
		once()
		if notices(tid) != before {
			t.Fatalf("reminded %d trials that must be left alone", notices(tid)-before)
		}
	})

	t.Run("the notice is tenant data: another tenant cannot see it", func(t *testing.T) {
		err := pgxx.InTenantTx(ctx, e.appDB, oid, func(tx pgx.Tx) error {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM subscription_trial_notices WHERE tenant_id = $1`, tid).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("tenant sees %d notices of another", n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
