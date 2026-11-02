//go:build integration

package itest

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// testContractRefundResultAndLog covers the leftovers of docs/plans/21-contracts.md: the result of a credit note's refund is copied onto the withdrawal that asked for it
// (once, in order, never onto another tenant's), a question about named kinds ignores the seller's setting, and the acceptance log is paged and filtered.
func testContractRefundResultAndLog(t *testing.T) {
	e := newEnv(t)
	live := e.account(t, "Contract results")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	ctx := as(live)
	c := e.contracts
	for _, cu := range []*plansv1.CreateCustomerRequest{{ExternalId: "bob", Name: "Bob", Email: "bob@example.com", Locale: "en"}, {ExternalId: "eve", Name: "Eve", Email: "eve@example.com", Locale: "en"}} {
		if _, err := e.plans.CreateCustomer(ctx, cu); err != nil {
			t.Fatal(err)
		}
	}
	tos := publishContract(t, e, live, "tos", "", true, map[string]string{"en": "Terms for {{customer_name}}"})
	privacy := publishContract(t, e, live, "privacy", "", true, map[string]string{"en": "Privacy for {{customer_name}}"})

	event := func(typ, creditNote, refundError string) {
		t.Helper()
		data := fmt.Sprintf(`{"credit_note_id":%q,"invoice_id":%q,"refund_state":"x","refund_error":%q}`, creditNote, uuid.NewString(), refundError)
		if _, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: typ, DataJson: data}); err != nil {
			t.Fatal(err)
		}
	}
	withdrawal := func(id string) *plansv1.ContractWithdrawal {
		t.Helper()
		l, err := c.ListContractWithdrawals(ctx, &plansv1.ListContractWithdrawalsRequest{CustomerExternalId: "bob"})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range l.GetWithdrawals() {
			if w.GetId() == id {
				return w
			}
		}
		t.Fatalf("withdrawal %s not listed", id)
		return nil
	}
	// requested makes an accepted withdrawal whose refund billing answered with a credit note.
	requested := func(cn string) *plansv1.ContractWithdrawal {
		t.Helper()
		w, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "bob", Statement: "I withdraw", InvoiceId: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		id := w.GetWithdrawal().GetId()
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: id, Status: "accepted"}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		r, err := c.RecordContractWithdrawalRefund(ctx, &plansv1.RecordContractWithdrawalRefundRequest{Id: id, State: "requested", CreditNoteId: cn})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetWithdrawal()
	}

	t.Run("the refund of the credit note is copied onto the withdrawal, once and in order", func(t *testing.T) {
		cn := uuid.NewString()
		w := requested(cn)
		event(webhooksmodel.CreditNoteRefundFailed, uuid.NewString(), "someone else's")
		event(webhooksmodel.InvoicePaid, cn, "")
		if got := withdrawal(w.GetId()); got.GetRefundState() != "requested" {
			t.Fatalf("a note that is not this withdrawal's, or an event that is not a refund result, changes nothing: %v", got)
		}

		event(webhooksmodel.CreditNoteRefundFailed, cn, "psp_declined: the card is closed")
		got := withdrawal(w.GetId())
		if got.GetRefundState() != "refund_failed" || got.GetRefundNote() != "psp_declined: the card is closed" || got.GetCreditNoteId() != cn {
			t.Fatalf("a failed refund: %v", got)
		}
		_, err := c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()})
		wantCode(t, "a second credit note after a failed refund", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
		event(webhooksmodel.CreditNoteRefundFailed, cn, "psp_declined: again")
		if got := withdrawal(w.GetId()); got.GetRefundNote() != "psp_declined: the card is closed" {
			t.Errorf("a repeated event changes nothing: %v", got)
		}

		// The seller retried the note's refund in billing and it went through.
		event(webhooksmodel.CreditNoteRefunded, cn, "")
		got = withdrawal(w.GetId())
		if got.GetRefundState() != "refunded" || got.GetRefundNote() != "" || got.GetCreditNoteId() != cn || got.GetStatus() != "accepted" {
			t.Fatalf("a refunded credit note: %v (the decision stays the seller's)", got)
		}
		// A failure event that arrives after it (reordered or repeated) never takes it back.
		event(webhooksmodel.CreditNoteRefundFailed, cn, "late")
		event(webhooksmodel.CreditNoteRefunded, cn, "")
		if got := withdrawal(w.GetId()); got.GetRefundState() != "refunded" {
			t.Errorf("refunded is final: %v", got)
		}
		_, err = c.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: w.GetId()})
		wantCode(t, "a refunded withdrawal is not refunded again", err, codes.FailedPrecondition, "plans.contract.refund_not_possible")
	})

	t.Run("a refund that went through at once is copied too, and another tenant's note is not", func(t *testing.T) {
		cn := uuid.NewString()
		w := requested(cn)
		other := e.account(t, "Other results")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
		if _, err := e.hooks.RecordTenantEvent(as(other), &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: webhooksmodel.CreditNoteRefunded,
			DataJson: fmt.Sprintf(`{"credit_note_id":%q}`, cn)}); err != nil {
			t.Fatal(err)
		}
		if got := withdrawal(w.GetId()); got.GetRefundState() != "requested" {
			t.Fatalf("an event of another tenant: %v", got)
		}
		event(webhooksmodel.CreditNoteRefunded, cn, "")
		if got := withdrawal(w.GetId()); got.GetRefundState() != "refunded" || got.GetRefundTime() == nil {
			t.Fatalf("requested to refunded: %v", got)
		}
		// An event with no credit note id is read and left alone, not retried for ever.
		if _, err := e.hooks.RecordTenantEvent(ctx, &plansv1.RecordTenantEventRequest{EventId: uuid.NewString(), Type: webhooksmodel.CreditNoteRefunded, DataJson: `{"invoice_id":"x"}`}); err != nil {
			t.Errorf("an event without a credit note id: %v", err)
		}
	})

	t.Run("a question about named kinds ignores the seller's setting", func(t *testing.T) {
		ask := func(kinds ...string) *plansv1.GetContractRequirementsResponse {
			t.Helper()
			r, err := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "eve", RequiredKinds: kinds})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		if r := ask(); r.GetEnforced() || !r.GetSatisfied() {
			t.Fatalf("a seller that requires nothing: %v", r)
		}
		r := ask("privacy")
		if !r.GetEnforced() || r.GetSatisfied() || len(r.GetRequirements()) != 1 || r.GetRequirements()[0].GetKind() != "privacy" {
			t.Fatalf("a link that names privacy: %v", r)
		}
		if _, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}}); err != nil {
			t.Fatal(err)
		}
		if r := ask("privacy"); len(r.GetRequirements()) != 1 || r.GetRequirements()[0].GetKind() != "privacy" {
			t.Fatalf("the link's list replaces the setting, it does not add to it: %v", r)
		}
		if r := ask(); len(r.GetRequirements()) != 1 || r.GetRequirements()[0].GetKind() != "tos" {
			t.Fatalf("no list: the setting: %v", r)
		}
		if _, err := c.RecordContractAcceptance(ctx, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "eve", Source: "api", Items: []*plansv1.ContractAcceptanceItem{{VersionId: privacy.GetId(), CheckboxText: "ok"}}}); err != nil {
			t.Fatal(err)
		}
		if r := ask("privacy"); !r.GetSatisfied() {
			t.Errorf("accepted what the link names: %v", r)
		}
		if r := ask(); r.GetSatisfied() {
			t.Errorf("the setting still wants the terms: %v", r)
		}
		_, err := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "eve", RequiredKinds: []string{"nda"}})
		wantCode(t, "a kind that does not exist", err, codes.InvalidArgument, "plans.request.invalid")
	})

	t.Run("the acceptance log is paged and filtered", func(t *testing.T) {
		base := time.Now().Add(-48 * time.Hour).Truncate(time.Microsecond)
		// Six acceptances for bob, one an hour apart: the terms by the API at hours 0, 2 and 4, the privacy notice reported by the console-less API at hours 1, 3 and 5.
		for h := 0; h < 6; h++ {
			v := tos
			if h%2 == 1 {
				v = privacy
			}
			if _, err := c.RecordContractAcceptance(ctx, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "bob", Source: "api", AcceptTime: timestamppb.New(base.Add(time.Duration(h) * time.Hour)),
				Items: []*plansv1.ContractAcceptanceItem{{VersionId: v.GetId(), CheckboxText: "ok"}}}); err != nil {
				t.Fatal(err)
			}
		}
		list := func(r *plansv1.ListContractAcceptancesRequest) *plansv1.ListContractAcceptancesResponse {
			t.Helper()
			r.CustomerExternalId = "bob"
			out, err := c.ListContractAcceptances(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		all := list(&plansv1.ListContractAcceptancesRequest{})
		if len(all.GetAcceptances()) != 6 || all.GetNextPageToken() != "" || all.GetAcceptances()[0].GetKind() != "privacy" {
			t.Fatalf("everything fits in the default page, newest first: %d %q", len(all.GetAcceptances()), all.GetNextPageToken())
		}
		// Pages of two cover the six exactly once, in order.
		var seen []string
		token := ""
		for pages := 0; pages < 4; pages++ {
			p := list(&plansv1.ListContractAcceptancesRequest{PageSize: 2, PageToken: token})
			for _, a := range p.GetAcceptances() {
				seen = append(seen, a.GetId())
			}
			if token = p.GetNextPageToken(); token == "" {
				break
			}
		}
		if len(seen) != 6 {
			t.Fatalf("three pages of two: %d rows", len(seen))
		}
		for i, a := range all.GetAcceptances() {
			if seen[i] != a.GetId() {
				t.Fatalf("page %d breaks the order", i)
			}
		}
		if got := list(&plansv1.ListContractAcceptancesRequest{Kind: "tos"}); len(got.GetAcceptances()) != 3 {
			t.Errorf("kind tos: %d", len(got.GetAcceptances()))
		}
		if got := list(&plansv1.ListContractAcceptancesRequest{VersionId: privacy.GetId()}); len(got.GetAcceptances()) != 3 {
			t.Errorf("one version: %d", len(got.GetAcceptances()))
		}
		if got := list(&plansv1.ListContractAcceptancesRequest{Source: "hosted"}); len(got.GetAcceptances()) != 0 {
			t.Errorf("source hosted: %d", len(got.GetAcceptances()))
		}
		window := list(&plansv1.ListContractAcceptancesRequest{FromTime: timestamppb.New(base.Add(1 * time.Hour)), ToTime: timestamppb.New(base.Add(4 * time.Hour))})
		if len(window.GetAcceptances()) != 3 {
			t.Errorf("from inclusive, to exclusive: hours 1, 2 and 3, got %d", len(window.GetAcceptances()))
		}
		for name, r := range map[string]*plansv1.ListContractAcceptancesRequest{
			"page size": {CustomerExternalId: "bob", PageSize: 201}, "negative": {CustomerExternalId: "bob", PageSize: -1}, "token": {CustomerExternalId: "bob", PageToken: "nope"},
			"kind": {CustomerExternalId: "bob", Kind: "nda"}, "source": {CustomerExternalId: "bob", Source: "carrier pigeon"}, "version": {CustomerExternalId: "bob", VersionId: "x"},
		} {
			_, err := c.ListContractAcceptances(ctx, r)
			wantCode(t, name, err, codes.InvalidArgument, "plans.request.invalid")
		}
	})
}
