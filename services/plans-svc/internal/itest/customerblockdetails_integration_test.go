//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	webhooksworker "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/worker"
)

// A block has a category and may end by itself; each block and lift is kept with who made it; the seller's webhooks hear of both; a block that has run out stops
// holding without anybody lifting it; and a price change does not offer a blocked customer a new price.
func testCustomerBlockDetails(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Block details")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	for _, fee := range []int64{3000, 4000} {
		if fee == 3000 {
			if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: fee}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"cus_a", "cus_b"} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	events := func(id string) []*plansv1.CustomerBlockEvent {
		t.Helper()
		r, err := e.plans.ListCustomerBlockEvents(ctx, &plansv1.ListCustomerBlockEventsRequest{ExternalId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetEvents()
	}
	until := time.Now().Add(48 * time.Hour).Truncate(time.Microsecond)

	t.Run("the category and the end are validated and carried", func(t *testing.T) {
		_, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_a", Category: "rude"})
		wantCode(t, "an unknown category", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_a", BlockedUntil: timestamppb.New(time.Now().Add(-time.Minute))})
		wantCode(t, "an end in the past", err, codes.InvalidArgument, "plans.request.invalid")
		if len(events("cus_a")) != 0 {
			t.Fatal("a refused block must leave no history")
		}
		b, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_a", Reason: "stolen card", Category: "fraud", BlockedUntil: timestamppb.New(until), Actor: "ada@example.com"})
		c := b.GetCustomer()
		if err != nil || c.GetBlockCategory() != "fraud" || c.GetBlockReason() != "stolen card" || !c.GetBlockedUntil().AsTime().Equal(until) {
			t.Fatalf("block: %v %v", b, err)
		}
	})

	t.Run("the history says who, when and why, and a repeat adds nothing", func(t *testing.T) {
		if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_a", Reason: "again", Category: "abuse", Actor: "other@example.com"}); err != nil {
			t.Fatal(err)
		}
		ev := events("cus_a")
		if len(ev) != 1 || ev[0].GetAction() != "blocked" || ev[0].GetCategory() != "fraud" || ev[0].GetReason() != "stolen card" || ev[0].GetActor() != "ada@example.com" ||
			!ev[0].GetBlockedUntil().AsTime().Equal(until) || ev[0].GetCreateTime() == nil {
			t.Fatalf("history = %v", ev)
		}
		if got := events("cus_b"); len(got) != 0 {
			t.Fatalf("another customer's history = %v", got)
		}
		if _, err := e.plans.ListCustomerBlockEvents(ctx, &plansv1.ListCustomerBlockEventsRequest{ExternalId: "nobody"}); code(t, err) != codes.NotFound {
			t.Errorf("history of a customer that is not there: %v", err)
		}
	})

	t.Run("the block holds until it runs out, then the customer can buy and be blocked afresh", func(t *testing.T) {
		_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_a", PlanCode: "basic", PlanVersion: 1})
		wantCode(t, "a subscription while blocked", err, codes.FailedPrecondition, "plans.customer.blocked")
		// The block ran out an hour ago (time is moved in the row, as the clock of a test cannot reach the database's now()).
		if err := pgxx.InTenantTx(context.Background(), e.appDB, uuid.MustParse(live.GetId()), func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `UPDATE customers SET blocked_at = now() - interval '2 hours', blocked_until = now() - interval '1 hour' WHERE external_id = 'cus_a'`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_a", PlanCode: "basic", PlanVersion: 1}); err != nil {
			t.Fatalf("a subscription after the block ran out: %v", err)
		}
		again, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_a", Reason: "second time", Category: "chargeback", Actor: "boss@example.com"})
		if err != nil || again.GetCustomer().GetBlockCategory() != "chargeback" || again.GetCustomer().GetBlockedUntil() != nil || again.GetCustomer().GetBlockReason() != "second time" {
			t.Fatalf("a block after one that ran out starts afresh: %v %v", again, err)
		}
		if ev := events("cus_a"); len(ev) != 2 || ev[0].GetCategory() != "chargeback" {
			t.Fatalf("history = %v", ev)
		}
	})

	t.Run("a lift is kept, and lifting what is not blocked is not", func(t *testing.T) {
		if _, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_a", Actor: "boss@example.com"}); err != nil {
			t.Fatal(err)
		}
		ev := events("cus_a")
		if len(ev) != 3 || ev[0].GetAction() != "unblocked" || ev[0].GetActor() != "boss@example.com" {
			t.Fatalf("history = %v", ev)
		}
		if _, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_b"}); err != nil || len(events("cus_b")) != 0 {
			t.Fatalf("lifting a customer who was never blocked: %v, history %v", err, events("cus_b"))
		}
	})

	t.Run("the seller's webhooks hear of a block and of a lift", func(t *testing.T) {
		e.clearDeliveries(t)
		rcv := newReceiver(t)
		if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: rcv.URL, EventTypes: []string{"customer.blocked", "customer.unblocked"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_b", Reason: "no more", Category: "request", BlockedUntil: timestamppb.New(until)}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_b"}); err != nil { // a repeat: no second event
			t.Fatal(err)
		}
		if _, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_b"}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Minute)
		disp := &webhooksworker.Dispatcher{Pool: e.system, Places: e.places, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return now }}
		if n, err := disp.Once(ctx); err != nil || n != 2 {
			t.Fatalf("dispatcher claimed %d, %v; want the block and the lift", n, err)
		}
		got := map[string]map[string]any{}
		for _, c := range rcv.calls() {
			var env struct {
				Type string         `json:"type"`
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(c.body, &env); err != nil {
				t.Fatal(err)
			}
			got[env.Type] = env.Data
		}
		b, u := got["customer.blocked"], got["customer.unblocked"]
		if b == nil || b["customer_external_id"] != "cus_b" || b["category"] != "request" || b["reason"] != "no more" || b["blocked_until"] == nil || b["customer_id"] == "" {
			t.Errorf("customer.blocked data = %v", b)
		}
		if u == nil || u["customer_external_id"] != "cus_b" {
			t.Errorf("customer.unblocked data = %v", u)
		}
	})

	t.Run("a price change skips a blocked customer and says why", func(t *testing.T) {
		e2 := "cus_price"
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: e2, Name: e2, Email: e2 + "@example.com"}); err != nil {
			t.Fatal(err)
		}
		sub, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: e2, PlanCode: "basic", PlanVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: e2}); err != nil {
			t.Fatal(err)
		}
		r, err := e.plans.SchedulePriceChange(ctx, &plansv1.SchedulePriceChangeRequest{PlanCode: "basic", FromVersion: 1, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		reasons := map[string]string{}
		for _, s := range r.GetSkipped() {
			reasons[s.GetSubscriptionId()] = s.GetReason()
		}
		if reasons[sub.GetSubscription().GetId()] != "customer_blocked" {
			t.Fatalf("skipped = %v, want customer_blocked for the blocked customer's subscription", r.GetSkipped())
		}
	})
}
