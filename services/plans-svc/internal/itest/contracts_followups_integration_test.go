//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/contracts"
)

// testContractFollowups covers the follow-ups of docs/plans/21-contracts.md: the new settings and their defaults, the claim and record halves of routing a withdrawal to a
// refund (one caller at a time, never twice, a stale claim taken over), the decision webhook, the acknowledgement fact of a withdrawal made on a hosted page, an acceptance
// made on the portal, and the renewal notice (once per renewal, only for a customer who accepted, in the seller's window, with the seller's words).
func testContractFollowups(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Contract follow-ups")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	ctx := as(live)
	tid := uuid.MustParse(live)
	c := e.contracts
	if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/followups"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.CreatePlan(ctx, &plansv1.CreatePlanRequest{Code: "basic", Name: "basic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.plans.PublishPlanVersion(ctx, &plansv1.PublishPlanVersionRequest{PlanCode: "basic", BaseFeeMinor: 3000, Fees: yearlyFees("basic", 3000)}); err != nil {
		t.Fatal(err)
	}
	for _, cu := range []*plansv1.CreateCustomerRequest{
		{ExternalId: "ada", Name: "Ada Lovelace", Email: "ada@example.com", Locale: "tr", CustomerType: "individual"},
		{ExternalId: "bob", Name: "Bob", Email: "bob@example.com", Locale: "en"},
	} {
		if _, err := e.plans.CreateCustomer(ctx, cu); err != nil {
			t.Fatal(err)
		}
	}
	tos := publishContract(t, e, live, "tos", "", true, map[string]string{"en": "Terms for {{customer_name}}"})
	bob, err := e.plans.GetCustomer(ctx, &plansv1.GetCustomerRequest{ExternalId: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	bobID := bob.GetCustomer().GetId()
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := e.scan(context.Background(), live, query, append([]any{tid}, args...), &n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("settings: defaults, keep what is not sent, validate", func(t *testing.T) {
		got, err := c.GetContractSettings(ctx, &plansv1.GetContractSettingsRequest{})
		st := got.GetSettings()
		if err != nil || st.GetWithdrawalRefund() != "full" || st.GetRenewalNoticeDays() != 7 || !st.GetWithdrawalFunction() || len(st.GetRenewalCancelTexts()) != 0 {
			t.Fatalf("defaults for a seller that never saved: %v %v", st, err)
		}
		set, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RenewalNoticeDays: proto.Int32(10), WithdrawalRefund: "none",
			RenewalCancelTexts: map[string]string{"tr": "Panelden iptal edin.", "en": "Cancel in your account."}})
		st = set.GetSettings()
		if err != nil || st.GetRenewalNoticeDays() != 10 || st.GetWithdrawalRefund() != "none" || !st.GetWithdrawalFunction() || st.GetRenewalCancelTexts()["tr"] != "Panelden iptal edin." {
			t.Fatalf("set: %v %v", st, err)
		}
		// A request that says nothing about the new fields (an older client) changes none of them; a text goes when it is sent empty.
		set, err = c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RenewalCancelTexts: map[string]string{"en": ""}})
		st = set.GetSettings()
		if err != nil || st.GetRenewalNoticeDays() != 10 || st.GetWithdrawalRefund() != "none" || st.GetRenewalCancelTexts()["tr"] == "" || st.GetRenewalCancelTexts()["en"] != "" {
			t.Fatalf("an old client keeps the new fields: %v %v", st, err)
		}
		set, err = c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RenewalNoticeDays: proto.Int32(0), WithdrawalFunction: proto.Bool(false), WithdrawalRefund: "full"})
		st = set.GetSettings()
		if err != nil || st.GetRenewalNoticeDays() != 0 || st.GetWithdrawalFunction() || st.GetWithdrawalRefund() != "full" {
			t.Fatalf("0 days is off, and a false is a false: %v %v", st, err)
		}
		for name, req := range map[string]*plansv1.SetContractSettingsRequest{
			"refund":   {RequiredKinds: []string{"tos"}, WithdrawalRefund: "half"},
			"days":     {RequiredKinds: []string{"tos"}, RenewalNoticeDays: proto.Int32(61)},
			"negative": {RequiredKinds: []string{"tos"}, RenewalNoticeDays: proto.Int32(-1)},
			"language": {RequiredKinds: []string{"tos"}, RenewalCancelTexts: map[string]string{"xx": "x"}},
		} {
			_, err := c.SetContractSettings(ctx, req)
			wantCode(t, name, err, codes.InvalidArgument, "plans.request.invalid")
		}
	})

	t.Run("a refund is claimed by one caller, recorded once, retried after a failure and taken over when it is stale", func(t *testing.T) {
		inv := uuid.NewString()
		mk := func(invoice string) *plansv1.ContractWithdrawal {
			r, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "bob", Statement: "I withdraw", InvoiceId: invoice})
			if err != nil {
				t.Fatal(err)
			}
			return r.GetWithdrawal()
		}
		w := mk(inv)
		_, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()})
		wantCode(t, "a refund before the decision", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w.GetId(), Status: "accepted", DecidedBy: "owner"}); err != nil {
			t.Fatal(err)
		}
		claim, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()})
		if err != nil || claim.GetReclaimed() || claim.GetWithdrawal().GetRefundState() != "requesting" || claim.GetWithdrawal().GetInvoiceId() != inv {
			t.Fatalf("claim: %v %v", claim, err)
		}
		_, err = c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()})
		wantCode(t, "a second caller while the first is asking", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
		failed, err := c.RecordContractWithdrawalRefund(ctx, &plansv1.RecordContractWithdrawalRefundRequest{Id: w.GetId(), State: "failed", Note: "billing did not answer"})
		if err != nil || failed.GetWithdrawal().GetRefundState() != "failed" || failed.GetWithdrawal().GetRefundNote() != "billing did not answer" {
			t.Fatalf("failed: %v %v", failed, err)
		}
		_, err = c.RecordContractWithdrawalRefund(ctx, &plansv1.RecordContractWithdrawalRefundRequest{Id: w.GetId(), State: "outside"})
		wantCode(t, "recording with no open request", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
		if _, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()}); err != nil {
			t.Fatalf("a failed refund can be asked for again: %v", err)
		}
		cn := uuid.NewString()
		for name, req := range map[string]*plansv1.RecordContractWithdrawalRefundRequest{
			"a state that is not one":          {Id: w.GetId(), State: "done"},
			"requested without a credit note":  {Id: w.GetId(), State: "requested"},
			"a credit note with another state": {Id: w.GetId(), State: "outside", CreditNoteId: cn},
		} {
			_, err := c.RecordContractWithdrawalRefund(ctx, req)
			wantCode(t, name, err, codes.InvalidArgument, "plans.request.invalid")
		}
		done, err := c.RecordContractWithdrawalRefund(ctx, &plansv1.RecordContractWithdrawalRefundRequest{Id: w.GetId(), State: "requested", CreditNoteId: cn})
		if d := done.GetWithdrawal(); err != nil || d.GetRefundState() != "requested" || d.GetCreditNoteId() != cn || d.GetRefundTime() == nil {
			t.Fatalf("requested: %v %v", done, err)
		}
		_, err = c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()})
		wantCode(t, "never twice", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")

		// A claim whose owner died stands for a while, then another caller takes it over and is told so.
		w2 := mk(uuid.NewString())
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w2.GetId(), Status: "accepted"}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w2.GetId()}); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour)
		if err := pgxx.InTenantTx(context.Background(), e.appDB, tid, func(tx pgx.Tx) error {
			_, err := tx.Exec(context.Background(), `UPDATE contract_withdrawals SET refund_at = $1 WHERE id = $2`, old, uuid.MustParse(w2.GetId()))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		again, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w2.GetId()})
		if err != nil || !again.GetReclaimed() {
			t.Fatalf("a stale claim is taken over and says so: %v %v", again, err)
		}

		// No invoice named: nothing can be routed, and the seller refunds by hand. A rejected one is never refunded.
		w3 := mk("")
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w3.GetId(), Status: "accepted"}); err != nil {
			t.Fatal(err)
		}
		_, err = c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w3.GetId()})
		wantCode(t, "no invoice", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
		w4 := mk(uuid.NewString())
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w4.GetId(), Status: "rejected"}); err != nil {
			t.Fatal(err)
		}
		_, err = c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w4.GetId()})
		wantCode(t, "rejected", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
		_, err = c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: uuid.NewString()})
		wantCode(t, "unknown", err, codes.NotFound, "plans.contract.withdrawal_not_found")

		if n := count(`SELECT count(*) FROM webhook_events WHERE tenant_id = $1 AND type = 'contract.withdrawal_decided'`); n < 4 {
			t.Errorf("every decision is announced: %d events", n)
		}
	})

	t.Run("a withdrawal made on a hosted page leaves the fact the acknowledgement mail is made from, one made by the seller does not", func(t *testing.T) {
		before := count(`SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = $2`, contracts.WithdrawalReceivedTopic)
		hosted, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerId: bobID, Statement: "Name: Bob\nContract: invoice 7", Source: "hosted"})
		if err != nil || hosted.GetWithdrawal().GetSource() != "hosted" || hosted.GetWithdrawal().GetCustomerExternalId() != "bob" {
			t.Fatalf("hosted withdrawal: %v %v", hosted, err)
		}
		if _, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "bob", Source: "api"}); err != nil {
			t.Fatal(err)
		}
		if n := count(`SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = $2`, contracts.WithdrawalReceivedTopic); n != before+1 {
			t.Fatalf("one acknowledgement for the hosted withdrawal, none for the one the seller recorded: %d -> %d", before, n)
		}
		var payload []byte
		if err := e.scan(context.Background(), live, `SELECT payload FROM outbox WHERE tenant_id = $1 AND topic = $2 ORDER BY id DESC LIMIT 1`, []any{tid, contracts.WithdrawalReceivedTopic}, &payload); err != nil {
			t.Fatal(err)
		}
		var ev eventsv1.ContractWithdrawalReceived
		if err := proto.Unmarshal(payload, &ev); err != nil || ev.GetEventId() != hosted.GetWithdrawal().GetId() || ev.GetStatement() != "Name: Bob\nContract: invoice 7" || ev.GetReceivedAt() == nil {
			t.Fatalf("the fact: %v %v", &ev, err)
		}
	})

	t.Run("an acceptance can be made on the portal, and only by the platform", func(t *testing.T) {
		item := []*plansv1.ContractAcceptanceItem{{VersionId: tos.GetId(), CheckboxText: "I accept"}}
		got, err := c.RecordContractAcceptance(ctx, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "bob", Source: "hosted", ContextKind: "portal", Locale: "en", Items: item, Ip: "198.51.100.4", UserAgent: "ua"})
		if err != nil || got.GetAcceptances()[0].GetContextKind() != "portal" || !got.GetAcceptances()[0].GetObserved()["ip"] {
			t.Fatalf("portal acceptance: %v %v", got, err)
		}
		_, err = c.RecordContractAcceptance(ctx, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "bob", Source: "api", ContextKind: "portal", Items: item})
		wantCode(t, "the seller cannot claim a portal acceptance", err, codes.InvalidArgument, "plans.request.invalid")
	})

	t.Run("the renewal notice: once per renewal, for a customer who accepted, inside the window, with the seller's words", func(t *testing.T) {
		if _, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RenewalNoticeDays: proto.Int32(7),
			RenewalCancelTexts: map[string]string{"tr": "Panelden iptal edin.", "en": "Cancel in your account."}}); err != nil {
			t.Fatal(err)
		}
		cur, _ := c.ListContractVersions(ctx, &plansv1.ListContractVersionsRequest{Kind: "tos"})
		start := time.Now().Add(-25 * 24 * time.Hour).Truncate(time.Microsecond)
		subscribe := func(ext string) *plansv1.Subscription {
			t.Helper()
			r, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: ext, PlanCode: "basic", StartTime: timestamppb.New(start)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.plans.AdvanceSubscriptionFee(ctx, &plansv1.AdvanceSubscriptionFeeRequest{SubscriptionId: r.GetSubscription().GetId(), PeriodStart: timestamppb.New(start)}); err != nil {
				t.Fatal(err)
			}
			return r.GetSubscription()
		}
		for _, ext := range []string{"ada"} {
			if _, err := c.RecordContractAcceptance(ctx, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: ext, Source: "api", Items: []*plansv1.ContractAcceptanceItem{{VersionId: cur.GetVersions()[0].GetId(), CheckboxText: "ok"}}}); err != nil {
				t.Fatal(err)
			}
		}
		subAda := subscribe("ada")
		// cy never accepted anything, so the gate is off while the subscription is made.
		if _, err := e.plans.CreateCustomer(ctx, &plansv1.CreateCustomerRequest{ExternalId: "cy", Name: "Cy", Email: "cy@example.com"}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{}); err != nil {
			t.Fatal(err)
		}
		subscribe("cy")
		if _, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}}); err != nil {
			t.Fatal(err)
		}

		renewsAt := start.AddDate(0, 1, 0)
		clock := renewsAt.Add(-3 * 24 * time.Hour)
		job := contracts.NewRenewer(e.places, e.appDB, slog.New(slog.NewTextHandler(io.Discard, nil))).WithClock(func() time.Time { return clock })
		facts := func() int {
			return count(`SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = $2`, contracts.RenewalTopic)
		}

		clock = renewsAt.Add(-9 * 24 * time.Hour)
		// The job looks across every tenant of the database, so what it says it noticed is not asserted here, only what it did for this one.
		if _, _, err := job.Once(context.Background()); err != nil || facts() != 0 {
			t.Fatalf("nine days ahead is outside the seller's 7: facts %d, %v", facts(), err)
		}
		clock = renewsAt.Add(-3 * 24 * time.Hour)
		if _, _, err := job.Once(context.Background()); err != nil || facts() != 1 {
			t.Fatalf("inside the window ada is noticed, cy (never accepted) is not: facts %d, %v", facts(), err)
		}
		if _, _, err := job.Once(context.Background()); err != nil || facts() != 1 {
			t.Fatalf("a second pass notices nothing: facts %d %v", facts(), err)
		}
		var payload []byte
		if err := e.scan(context.Background(), live, `SELECT payload FROM outbox WHERE tenant_id = $1 AND topic = $2`, []any{tid, contracts.RenewalTopic}, &payload); err != nil {
			t.Fatal(err)
		}
		var ev eventsv1.RenewalNotice
		if err := proto.Unmarshal(payload, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.GetSubscriptionId() != subAda.GetId() || ev.GetPriceMinor() != 3000 || ev.GetCurrency() != "TRY" || ev.GetCurrencyExponent() != 2 || ev.GetCancelText() != "Panelden iptal edin." || ev.GetInterval() != "month" ||
			!ev.GetRenewsAt().AsTime().Equal(renewsAt) || ev.GetPlanCode() != "basic" || ev.GetEventId() != contracts.RenewalEventID(tid, uuid.MustParse(subAda.GetCustomerId()), renewsAt).String() {
			t.Fatalf("the fact (in the customer's language, the price after the coupon, the date): %v", &ev)
		}
		var stored string
		if err := e.scan(context.Background(), live, `SELECT cancel_text FROM contract_renewal_notices WHERE tenant_id = $1`, []any{tid}, &stored); err != nil || stored != "Panelden iptal edin." {
			t.Errorf("the notice is stored as it was sent: %q %v", stored, err)
		}
		// Switched off: nothing, whatever the date.
		if _, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RenewalNoticeDays: proto.Int32(0)}); err != nil {
			t.Fatal(err)
		}
		clock = renewsAt.Add(-time.Hour)
		if err := e.exec(context.Background(), live, `DELETE FROM contract_renewal_notices WHERE tenant_id = $1`, tid); err != nil {
			t.Fatal(err)
		}
		if _, _, err := job.Once(context.Background()); err != nil || count(`SELECT count(*) FROM contract_renewal_notices WHERE tenant_id = $1`) != 0 {
			t.Errorf("0 days is off: %v", err)
		}
	})
}
