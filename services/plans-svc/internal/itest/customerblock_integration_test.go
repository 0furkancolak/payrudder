//go:build integration

package itest

import (
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// A blocked customer gets no new subscription; what exists runs on; the block keeps its first time; lifting it lets a subscription be made again.
func testCustomerBlock(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Blocking")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000, Fees: yearlyFees("basic", 3000)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cus_ok", "cus_bad"} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	// A subscription that exists before the block.
	before, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_bad", PlanCode: "basic"})
	if err != nil {
		t.Fatal(err)
	}

	b, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_bad", Reason: "chargebacks"})
	if err != nil || b.GetCustomer().GetBlockTime() == nil || b.GetCustomer().GetBlockReason() != "chargebacks" {
		t.Fatalf("block: %v %v", b, err)
	}
	first := b.GetCustomer().GetBlockTime().AsTime()
	again, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_bad", Reason: "something else"})
	if err != nil || !again.GetCustomer().GetBlockTime().AsTime().Equal(first) || again.GetCustomer().GetBlockReason() != "chargebacks" {
		t.Fatalf("blocking again changed the first time or note: %v %v", again, err)
	}

	_, err = e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_bad", PlanCode: "basic"})
	if code(t, err) != codes.FailedPrecondition {
		t.Fatalf("a subscription for a blocked customer: %v", err)
	}
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_ok", PlanCode: "basic"}); err != nil {
		t.Fatalf("another customer is not affected: %v", err)
	}
	if got, err := e.plans.GetSubscription(ctx, &plansv1.GetSubscriptionRequest{SubscriptionId: before.GetSubscription().GetId()}); err != nil || got.GetSubscription().GetState() == plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED {
		t.Fatalf("the existing subscription must run on: %v %v", got, err)
	}
	if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_bad", Reason: string(make([]byte, 501))}); code(t, err) != codes.InvalidArgument {
		t.Errorf("a note of 501 characters: %v", err)
	}
	if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "nobody"}); code(t, err) != codes.NotFound {
		t.Errorf("a customer that is not there: %v", err)
	}

	u, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_bad"})
	if err != nil || u.GetCustomer().GetBlockTime() != nil || u.GetCustomer().GetBlockReason() != "" {
		t.Fatalf("unblock: %v %v", u, err)
	}
	// A customer with no subscription yet: refused while blocked, accepted once the block is lifted.
	if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cus_late", Name: "late", Email: "late@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: "cus_late"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_late", PlanCode: "basic"}); code(t, err) != codes.FailedPrecondition {
		t.Fatalf("blocked: %v", err)
	}
	if _, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_late"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "cus_late", PlanCode: "basic"}); err != nil {
		t.Fatalf("after the block is lifted: %v", err)
	}
}
