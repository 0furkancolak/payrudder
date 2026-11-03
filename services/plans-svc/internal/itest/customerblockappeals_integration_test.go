//go:build integration

package itest

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// The appeal of a blocked customer (plan 13): one statement while the block is in force, one open at a time, accepted by the seller through the same lift as the seller's own
// (the history, customer.unblocked's audit entry, the customer's notice), rejected only with an answer, decided once; an appeal still open when the block is lifted by other
// means is closed; and an appeal never carries the seller's category or note.
func testCustomerBlockAppeals(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Block appeals")[plansv1.Environment_ENVIRONMENT_LIVE]
	ctx := as(live.GetId())
	for _, id := range []string{"cus_ok", "cus_other", "cus_lifted"} {
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: id, Name: id, Email: id + "@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	block := func(id string) {
		t.Helper()
		if _, err := e.plans.BlockCustomer(ctx, &plansv1.BlockCustomerRequest{ExternalId: id, Reason: "SELLER-NOTE", Category: "fraud", Actor: "boss@example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	submit := func(id, text string) (*plansv1.CustomerBlockAppeal, error) {
		r, err := e.plans.SubmitCustomerBlockAppeal(ctx, &plansv1.SubmitCustomerBlockAppealRequest{ExternalId: id, Statement: text})
		return r.GetAppeal(), err
	}
	decide := func(id, appeal, decision, answer string) (*plansv1.CustomerBlockAppeal, error) {
		r, err := e.plans.DecideCustomerBlockAppeal(ctx, &plansv1.DecideCustomerBlockAppealRequest{ExternalId: id, AppealId: appeal, Decision: decision, Answer: answer, Actor: "boss@example.com"})
		return r.GetAppeal(), err
	}
	list := func(id string) []*plansv1.CustomerBlockAppeal {
		t.Helper()
		r, err := e.plans.ListCustomerBlockAppeals(ctx, &plansv1.ListCustomerBlockAppealsRequest{ExternalId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetAppeals()
	}
	customer := func(id string) *plansv1.Customer {
		t.Helper()
		r, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetCustomer()
	}
	blocked := func(id string) bool {
		t.Helper()
		return customer(id).GetBlockTime() != nil
	}

	t.Run("there is nothing to appeal for a customer who is not blocked, and a statement must be 1 to 2000 characters", func(t *testing.T) {
		_, err := submit("cus_ok", "please")
		wantCode(t, "an appeal of a customer who is not blocked", err, codes.FailedPrecondition, "plans.customer_appeal.not_blocked")
		block("cus_ok")
		_, err = submit("cus_ok", " \n\t ")
		wantCode(t, "an empty statement", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = submit("cus_ok", strings.Repeat("a", 2001))
		wantCode(t, "a statement over 2000 characters", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = submit("nobody", "hello")
		wantCode(t, "a customer that is not there", err, codes.NotFound, "")
		if n := len(list("cus_ok")); n != 0 {
			t.Fatalf("refused appeals were kept: %d", n)
		}
		// 2000 characters of two-byte letters are fine: the limit counts characters.
		if a, err := submit("cus_ok", strings.Repeat("ş", 2000)); err != nil || a.GetStatus() != "open" {
			t.Fatalf("2000 characters: %v %v", a, err)
		}
	})

	t.Run("one appeal is open at a time, kept as written, without the seller's note", func(t *testing.T) {
		_, err := submit("cus_ok", "a second one")
		wantCode(t, "a second open appeal", err, codes.AlreadyExists, "plans.customer_appeal.exists")
		got := list("cus_ok")
		if len(got) != 1 || got[0].GetStatus() != "open" || got[0].GetCreateTime() == nil || got[0].GetDecideTime() != nil {
			t.Fatalf("appeals = %v", got)
		}
		if b, _ := proto.Marshal(got[0]); strings.Contains(string(b), "SELLER-NOTE") || strings.Contains(string(b), "fraud") {
			t.Errorf("the appeal carries the block's note or category: %v", got[0])
		}
		if n := len(list("cus_other")); n != 0 {
			t.Errorf("another customer's appeals = %d", n)
		}
		logged := e.events(t, ctx, &auditv1.ListAuditEventsRequest{Actions: []string{"customer_block.appeal_submitted"}, PageSize: 20})
		if len(logged) != 1 {
			t.Errorf("audit entries of the submission = %d, want 1", len(logged))
		}
	})

	t.Run("rejecting needs an answer, keeps the block, and a rejected appeal is decided once", func(t *testing.T) {
		ap := list("cus_ok")[0]
		_, err := decide("cus_ok", ap.GetId(), "rejected", "  ")
		wantCode(t, "a rejection without an answer", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = decide("cus_ok", ap.GetId(), "maybe", "")
		wantCode(t, "an unknown decision", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = decide("cus_ok", ap.GetId(), "rejected", strings.Repeat("a", 1001))
		wantCode(t, "an answer over 1000 characters", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = decide("cus_other", ap.GetId(), "rejected", "no")
		wantCode(t, "another customer's appeal", err, codes.NotFound, "")
		out, err := decide("cus_ok", ap.GetId(), "rejected", "We checked.")
		if err != nil || out.GetStatus() != "rejected" || out.GetAnswer() != "We checked." || out.GetDecidedBy() != "boss@example.com" || out.GetDecideTime() == nil {
			t.Fatalf("rejecting: %v %v", out, err)
		}
		if !blocked("cus_ok") {
			t.Fatal("a rejection lifted the block")
		}
		_, err = decide("cus_ok", ap.GetId(), "accepted", "")
		wantCode(t, "deciding twice", err, codes.FailedPrecondition, "plans.customer_appeal.not_open")
		if !blocked("cus_ok") {
			t.Fatal("deciding twice lifted the block")
		}
	})

	t.Run("accepting lifts the block through the same path: history, audit, notice; the customer may be blocked and appeal again", func(t *testing.T) {
		second, err := submit("cus_ok", "second appeal after the rejection")
		if err != nil {
			t.Fatalf("an appeal after a rejection: %v", err)
		}
		out, err := decide("cus_ok", second.GetId(), "accepted", "")
		if err != nil || out.GetStatus() != "accepted" {
			t.Fatalf("accepting: %v %v", out, err)
		}
		if blocked("cus_ok") {
			t.Fatal("the customer is still blocked after an accepted appeal")
		}
		ev, err := e.plans.ListCustomerBlockEvents(ctx, &plansv1.ListCustomerBlockEventsRequest{ExternalId: "cus_ok"})
		if err != nil || len(ev.GetEvents()) != 2 || ev.GetEvents()[0].GetAction() != "unblocked" || ev.GetEvents()[0].GetActor() != "boss@example.com" {
			t.Fatalf("block history = %v %v", ev, err)
		}
		for _, action := range []string{"customer.unblocked", "customer_block.appeal_decided"} {
			if n := len(e.events(t, ctx, &auditv1.ListAuditEventsRequest{Actions: []string{action}, PageSize: 20})); n == 0 {
				t.Errorf("no audit entry %s", action)
			}
		}
		sink := &captureSink{}
		e.relayAll(t, sink)
		unblocked := 0
		for _, row := range sink.rows {
			n := &eventsv1.CustomerBlockNotice{}
			if row.Topic == "customer.block-notice.v1" && proto.Unmarshal(row.Payload, n) == nil && n.GetKind() == "unblocked" && strings.HasSuffix(row.PartitionKey, "/"+customer("cus_ok").GetId()) {
				unblocked++
			}
		}
		if unblocked != 1 {
			t.Errorf("unblock notices for the customer = %d, want 1", unblocked)
		}
		_, err = submit("cus_ok", "after the lift")
		wantCode(t, "an appeal after the lift", err, codes.FailedPrecondition, "plans.customer_appeal.not_blocked")
		_, err = decide("cus_ok", second.GetId(), "accepted", "")
		wantCode(t, "accepting twice", err, codes.FailedPrecondition, "plans.customer_appeal.not_open")
	})

	t.Run("an appeal still open when the block is lifted by the seller is closed", func(t *testing.T) {
		block("cus_lifted")
		ap, err := submit("cus_lifted", "please")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.plans.UnblockCustomer(ctx, &plansv1.UnblockCustomerRequest{ExternalId: "cus_lifted", Actor: "boss@example.com"}); err != nil {
			t.Fatal(err)
		}
		got := list("cus_lifted")
		if len(got) != 1 || got[0].GetStatus() != "closed" || got[0].GetDecidedBy() != "system" || got[0].GetDecideTime() == nil {
			t.Fatalf("appeals after a lift = %v", got)
		}
		_, err = decide("cus_lifted", ap.GetId(), "accepted", "")
		wantCode(t, "deciding a closed appeal", err, codes.FailedPrecondition, "plans.customer_appeal.not_open")
	})
}
