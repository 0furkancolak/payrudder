//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	customerscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/customers/command"
	webhooksworker "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/worker"
)

// A block that ran out by itself is cleared by the sweep: the row, the history ("system"), the end of a suspension and customer.unblocked, once; a block that still
// holds, or has no end, is left alone. And the seller's own call for a blocked customer (a coupon redeemed by hand) works: row 12 of the path-by-gate table in
// docs/plans/13-customer-ban.md.
func testCustomerBlockSweep(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Block sweep")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	tid := uuid.MustParse(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateCoupon(ctx, &plansv1.CreateCouponRequest{Coupon: &plansv1.Coupon{Code: "TEN", PercentBps: 1000, Duration: plansv1.CouponDuration_COUPON_DURATION_ONCE}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cus_ended", "cus_running", "cus_forever", "cus_plain"} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_forever", PlanCode: "basic"})
	if err != nil {
		t.Fatal(err)
	}
	until := timestamppb.New(time.Now().Add(48 * time.Hour))
	for _, b := range []*plansv1.BlockCustomerRequest{
		{ExternalId: "cus_ended", Category: "abuse", BlockedUntil: until, SuspendService: true},
		{ExternalId: "cus_running", Category: "abuse", BlockedUntil: until},
		{ExternalId: "cus_forever", Category: "fraud", SuspendService: true},
	} {
		if _, err := e.plans.BlockCustomer(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	created, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: "cus_ended"})
	if err != nil {
		t.Fatal(err)
	}
	key := tid.String() + "/" + created.GetCustomer().GetId() + "/"
	// Time is moved in the row, as the clock of a test cannot reach the database's now(): the block ran out an hour ago.
	if err := pgxx.InTenantTx(context.Background(), e.appDB, tid, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `UPDATE customers SET blocked_at = now() - interval '2 hours', blocked_until = now() - interval '1 hour' WHERE external_id = 'cus_ended'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sink := &captureSink{}
	e.relayAll(t, sink) // drain what the blocks published
	e.clearDeliveries(t)
	rcv := newReceiver(t)
	if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: rcv.URL, EventTypes: []string{"customer.unblocked"}}); err != nil {
		t.Fatal(err)
	}

	// The sweep reads across tenants, so it also clears the blocks of the other tests that share this database: what a test asserts is its own tenant's rows.
	sweep := customerscmd.NewSweeper(e.system, e.appDB, e.places, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, _, err := sweep.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := func(id string) *plansv1.Customer {
		t.Helper()
		r, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetCustomer()
	}
	if c := state("cus_ended"); c.GetBlockTime() != nil || c.GetBlockedUntil() != nil || c.GetBlockCategory() != "" || c.GetServiceSuspended() {
		t.Fatalf("the block that ran out is still in the row: %v", c)
	}
	for _, id := range []string{"cus_running", "cus_forever"} {
		if state(id).GetBlockTime() == nil {
			t.Errorf("%s: a block that still holds was cleared", id)
		}
	}
	history, err := e.plans.ListCustomerBlockEvents(ctx, &plansv1.ListCustomerBlockEventsRequest{ExternalId: "cus_ended"})
	if err != nil || len(history.GetEvents()) != 2 || history.GetEvents()[0].GetAction() != "unblocked" || history.GetEvents()[0].GetActor() != "system" {
		t.Fatalf("history = %v, %v: want the block and then the lift by the system", history.GetEvents(), err)
	}
	if h, _ := e.plans.ListCustomerBlockEvents(ctx, &plansv1.ListCustomerBlockEventsRequest{ExternalId: "cus_running"}); len(h.GetEvents()) != 1 {
		t.Errorf("a block that holds got a lift in its history: %v", h.GetEvents())
	}
	sink = &captureSink{}
	e.relayAll(t, sink)
	var tombstones int
	for _, row := range sink.rows {
		if row.Topic == "plans.entitlements.v1" && row.PartitionKey == key && row.Payload == nil {
			tombstones++
		}
		if row.Topic == "plans.entitlements.v1" && row.PartitionKey == key && row.Payload != nil {
			var rec eventsv1.EntitlementState
			if err := proto.Unmarshal(row.Payload, &rec); err != nil || rec.GetSuspended() {
				t.Errorf("a suspension was published by the sweep: %v %v", &rec, err)
			}
		}
	}
	if tombstones != 1 {
		t.Errorf("tombstones of the suspension = %d, want 1", tombstones)
	}

	// The webhook is announced once, and a second pass finds nothing to do.
	if n, _, err := sweep.Once(context.Background()); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Errorf("a second pass lifted %d (its own tenant's block is gone; others have none left either)", n)
	}
	now := time.Now().Add(time.Minute)
	disp := &webhooksworker.Dispatcher{Pool: e.system, Places: e.places, Box: e.box, Client: safehttp.NewClient(2*time.Second, true), Now: func() time.Time { return now }}
	if _, err := disp.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sent int
	for _, c := range rcv.calls() {
		var env struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(c.body, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type == "customer.unblocked" && env.Data["customer_external_id"] == "cus_ended" {
			sent++
		}
	}
	if sent != 1 {
		t.Errorf("customer.unblocked for the block that ended was delivered %d times, want 1", sent)
	}

	t.Run("the seller's own call for a blocked customer works: a coupon by hand (row 12)", func(t *testing.T) {
		if state("cus_forever").GetBlockTime() == nil {
			t.Fatal("the customer is not blocked")
		}
		ap, err := e.plans.ApplyCoupon(ctx, &plansv1.ApplyCouponRequest{SubscriptionId: sub.GetSubscription().GetId(), Code: "TEN"})
		if err != nil || ap.GetSubscription().GetCouponCode() != "TEN" {
			t.Fatalf("a coupon for a customer blocked for fraud with the service suspended: %v, %v", ap, err)
		}
	})
}
