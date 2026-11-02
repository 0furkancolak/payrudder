//go:build integration

package itest

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/contracts"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// publish publishes a version and fails the test when it is refused.
func publishContract(t *testing.T, e *env, tenantID, kind, audience string, material bool, bodies map[string]string) *plansv1.ContractVersion {
	t.Helper()
	v, err := e.contracts.PublishContractVersion(as(tenantID), &plansv1.PublishContractVersionRequest{Kind: kind, Audience: audience, Bodies: bodies, Material: material, Author: "itest"})
	if err != nil {
		t.Fatal(err)
	}
	return v.GetVersion()
}

func accept(t *testing.T, e *env, tenantID string, req *plansv1.RecordContractAcceptanceRequest) (*plansv1.RecordContractAcceptanceResponse, error) {
	t.Helper()
	return e.contracts.RecordContractAcceptance(as(tenantID), req)
}

// testContracts covers docs/plans/21-contracts.md: versions that cannot be edited, the gate at subscription creation, the evidence of an acceptance and the page that was
// hashed, the two consents, the audience of a business customer, the append-only table and the one thing that may change it, the withdrawal record and its events.
func testContracts(t *testing.T) {
	e := newEnv(t)
	acct := e.account(t, "Contracts Co")
	live := acct[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	ctx := as(live)
	c := e.contracts
	if _, err := e.hooks.CreateWebhookEndpoint(ctx, &plansv1.CreateWebhookEndpointRequest{Url: "https://example.com/contracts"}); err != nil {
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
		{ExternalId: "acme", Name: "Acme Ltd", Email: "ap@acme.example", Locale: "en", CustomerType: "business"},
		{ExternalId: "grace", Name: "Grace Hopper", Email: "grace@example.com"},
	} {
		if _, err := e.plans.CreateCustomer(ctx, cu); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a version is immutable and the series counts up", func(t *testing.T) {
		v1 := publishContract(t, e, live, "tos", "", false, map[string]string{"en": "Terms for {{customer_name}} at {{seller_name}}", "tr": "{{customer_name}} için koşullar"})
		if v1.GetNumber() != 1 || !v1.GetMaterial() || !v1.GetCurrent() || v1.GetAudience() != "consumer" || v1.GetSha256() != domain.ContractHash("tos", "consumer", v1.GetBodies()) {
			t.Fatalf("first version: %+v (the first of a series is material)", v1)
		}
		v2 := publishContract(t, e, live, "tos", "", false, map[string]string{"en": "Terms v2 for {{customer_name}}"})
		if v2.GetNumber() != 2 || v2.GetMaterial() {
			t.Fatalf("second version: %+v", v2)
		}
		oldResp, err := c.GetContractVersion(ctx, &plansv1.GetContractVersionRequest{Id: v1.GetId()})
		old := oldResp.GetVersion()
		if err != nil || old.GetCurrent() || old.GetBodies()["en"] != v1.GetBodies()["en"] {
			t.Fatalf("version 1 after version 2: %+v %v", old, err)
		}
		for _, bad := range []struct {
			name string
			req  *plansv1.PublishContractVersionRequest
			key  string
		}{
			{"unknown variable", &plansv1.PublishContractVersionRequest{Kind: "tos", Bodies: map[string]string{"en": "{{card_number}}"}}, "plans.contract.unknown_variable"},
			{"no text", &plansv1.PublishContractVersionRequest{Kind: "tos"}, "plans.contract.body_required"},
			{"bad kind", &plansv1.PublishContractVersionRequest{Kind: "nda", Bodies: map[string]string{"en": "x"}}, "plans.request.invalid"},
			{"bad language", &plansv1.PublishContractVersionRequest{Kind: "tos", Bodies: map[string]string{"xx": "x"}}, "plans.request.invalid"},
			{"bad audience", &plansv1.PublishContractVersionRequest{Kind: "tos", Audience: "alien", Bodies: map[string]string{"en": "x"}}, "plans.request.invalid"},
		} {
			_, err := c.PublishContractVersion(ctx, bad.req)
			wantCode(t, bad.name, err, codes.InvalidArgument, bad.key)
		}
		for _, stmt := range []string{`UPDATE contract_versions SET note = 'edited'`, `DELETE FROM contract_versions`} {
			err := pgxx.InTenantTx(context.Background(), e.appDB, uuid.MustParse(live), func(tx pgx.Tx) error { _, err := tx.Exec(context.Background(), stmt); return err })
			if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
				t.Errorf("%s as the application role: want 42501, got %v", stmt, err)
			}
			if err := e.exec(context.Background(), live, stmt); pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
				t.Errorf("%s as the system role: want 42501, got %v", stmt, err)
			}
		}
		list, err := c.ListContractVersions(ctx, &plansv1.ListContractVersionsRequest{Kind: "tos"})
		if err != nil || len(list.GetVersions()) != 2 || list.GetVersions()[0].GetNumber() != 2 {
			t.Errorf("list: %v %v", list, err)
		}
	})

	t.Run("a tenant sees only its own", func(t *testing.T) {
		other := e.account(t, "Other Contracts Co")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
		l, err := c.ListContractVersions(as(other), &plansv1.ListContractVersionsRequest{})
		if err != nil || len(l.GetVersions()) != 0 {
			t.Fatalf("another tenant's list: %v %v", l, err)
		}
		cur, _ := c.ListContractVersions(ctx, &plansv1.ListContractVersionsRequest{Kind: "tos"})
		_, err = c.GetContractVersion(as(other), &plansv1.GetContractVersionRequest{Id: cur.GetVersions()[0].GetId()})
		wantCode(t, "another tenant's version", err, codes.NotFound, "plans.contract.version_not_found")
	})

	t.Run("settings: a kind is required only once it is published", func(t *testing.T) {
		_, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"privacy"}})
		wantCode(t, "privacy unpublished", err, codes.FailedPrecondition, "plans.contract.kind_unpublished")
		_, err = c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos"}, RetentionMonths: 6})
		wantCode(t, "retention below a year", err, codes.InvalidArgument, "plans.request.invalid")
		stResp, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"tos", "tos"}})
		st := stResp.GetSettings()
		if err != nil || len(st.GetRequiredKinds()) != 1 || st.GetRetentionMonths() != 36 || len(st.GetPublishedKinds()) != 1 {
			t.Fatalf("settings: %v %v", st, err)
		}
	})

	t.Run("the gate: no subscription before the required agreement is accepted", func(t *testing.T) {
		_, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "ada", PlanCode: "basic"})
		wantCode(t, "subscription before acceptance", err, codes.FailedPrecondition, "plans.contract.not_accepted")
		req, err := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "ada", Locale: "tr", Order: &plansv1.ContractOrder{Description: "Basic", Total: "30.00", Currency: "USD"}})
		if err != nil || !req.GetEnforced() || req.GetSatisfied() || len(req.GetRequirements()) != 1 || req.GetRequirements()[0].GetAccepted() {
			t.Fatalf("requirements: %v %v", req, err)
		}
		r := req.GetRequirements()[0]
		if r.GetLocale() != "en" || !strings.Contains(r.GetBodyHtml(), "Terms v2 for Ada Lovelace") || r.GetVersionNumber() != 2 {
			t.Fatalf("the current text in the language it exists in, with the customer's name: %+v", r)
		}
		// The first version is no longer the one to accept.
		list, _ := c.ListContractVersions(ctx, &plansv1.ListContractVersionsRequest{Kind: "tos"})
		_, err = accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "ada", Source: "api", Items: []*plansv1.ContractAcceptanceItem{{VersionId: list.GetVersions()[1].GetId(), CheckboxText: "I accept"}}})
		wantCode(t, "a stale version", err, codes.FailedPrecondition, "plans.contract.version_stale")
		_, err = accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "ada", Source: "api", Items: []*plansv1.ContractAcceptanceItem{{VersionId: r.GetVersionId()}}})
		wantCode(t, "words next to the box missing", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "ada", Source: "browser", Items: []*plansv1.ContractAcceptanceItem{{VersionId: r.GetVersionId(), CheckboxText: "x"}}})
		wantCode(t, "a source that is not one", err, codes.InvalidArgument, "plans.request.invalid")

		got, err := accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "ada", Source: "api", Locale: "tr", ContextRef: "order-77", Ip: "203.0.113.9", UserAgent: "checkout/1",
			Items: []*plansv1.ContractAcceptanceItem{{VersionId: r.GetVersionId(), CheckboxText: "Koşulları okudum ve kabul ediyorum"}}})
		if err != nil || len(got.GetAcceptances()) != 1 {
			t.Fatalf("accept: %v %v", got, err)
		}
		a := got.GetAcceptances()[0]
		if a.GetObserved()["ip"] || a.GetObserved()["text"] || !a.GetObserved()["time"] || a.GetSource() != "api" || a.GetIp() != "203.0.113.9" || a.GetLocaleRequested() != "tr" ||
			a.GetLocaleServed() != "en" || a.GetBodySha256() != domain.TextHash("Terms v2 for {{customer_name}}") || a.GetVersionNumber() != 2 || a.GetVariables()["customer_name"] != "Ada Lovelace" {
			t.Fatalf("evidence of an API acceptance: the seller's claims are marked as claims: %+v", a)
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "ada", PlanCode: "basic"}); err != nil {
			t.Fatalf("subscription after acceptance: %v", err)
		}
		// A customer who has not accepted is still refused: the acceptance is the customer's.
		_, err = e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "grace", PlanCode: "basic"})
		wantCode(t, "another customer", err, codes.FailedPrecondition, "plans.contract.not_accepted")

		// A minor version asks nobody again; a material one asks everybody.
		publishContract(t, e, live, "tos", "", false, map[string]string{"en": "Terms v3, a typo fixed"})
		if req, _ := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "ada"}); !req.GetSatisfied() {
			t.Error("a minor version made the customer accept again")
		}
		publishContract(t, e, live, "tos", "", true, map[string]string{"en": "Terms v4, new fees"})
		if req, _ := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "ada"}); req.GetSatisfied() {
			t.Error("a material version did not ask the customer again")
		}
		// Renewals are not gated: nothing here calls the gate for an existing subscription, and the existing one is untouched.
		subs, err := e.plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{})
		if err != nil || len(subs.GetSubscriptions()) != 1 {
			t.Errorf("the subscription made before the new version: %v %v", subs, err)
		}
	})

	t.Run("a business customer gets the business text, an unknown type the consumer's", func(t *testing.T) {
		biz := publishContract(t, e, live, "tos", "business", false, map[string]string{"en": "B2B terms for {{customer_name}}"})
		req, err := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "acme"})
		if err != nil || req.GetAudience() != "business" || req.GetRequirements()[0].GetVersionId() != biz.GetId() || !strings.Contains(req.GetRequirements()[0].GetBodyHtml(), "B2B terms for Acme Ltd") {
			t.Fatalf("business requirements: %v %v", req, err)
		}
		req, _ = c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "grace"})
		if req.GetAudience() != "consumer" || strings.Contains(req.GetRequirements()[0].GetBodyHtml(), "B2B") {
			t.Fatalf("a customer of unknown type: %v", req)
		}
		// The frozen type of an invoice overrides the customer's own (the hosted page passes it).
		req, _ = c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "grace", CustomerType: "business"})
		if req.GetAudience() != "business" {
			t.Errorf("customer_type of the request: %v", req.GetAudience())
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "acme", PlanCode: "basic"}); code(t, err) != codes.FailedPrecondition {
			t.Errorf("a business customer is gated too: %v", err)
		}
	})

	t.Run("the two consents are separate and required when the seller asks", func(t *testing.T) {
		publishContract(t, e, live, "distance_sales", "", true, map[string]string{"en": "Distance sales for {{customer_name}}: {{order_description}} {{order_total}} {{order_currency}}"})
		if _, err := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"distance_sales", "tos"}, ImmediateStart: true}); err != nil {
			t.Fatal(err)
		}
		req, _ := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "grace", Order: &plansv1.ContractOrder{Description: "Basic", Total: "30.00", Currency: "USD"}})
		if !req.GetImmediateStart() || len(req.GetRequirements()) != 2 || req.GetRequirements()[0].GetKind() != "distance_sales" {
			t.Fatalf("a consumer is asked for the consents and sees the contract first: %v", req)
		}
		if biz, _ := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "acme"}); biz.GetImmediateStart() {
			t.Error("a business customer is not asked to give up a right of withdrawal it does not have")
		}
		items := []*plansv1.ContractAcceptanceItem{{VersionId: req.GetRequirements()[0].GetVersionId(), CheckboxText: "I accept the contract"}, {VersionId: req.GetRequirements()[1].GetVersionId(), CheckboxText: "I accept the terms"}}
		for name, r := range map[string]*plansv1.RecordContractAcceptanceRequest{
			"no consents":            {CustomerExternalId: "grace", Source: "hosted", Items: items},
			"only the request":       {CustomerExternalId: "grace", Source: "hosted", Items: items, ImmediateStartConsent: true, ImmediateStartText: "Start now"},
			"consents without words": {CustomerExternalId: "grace", Source: "hosted", Items: items, ImmediateStartConsent: true, WithdrawalWaiverAcknowledged: true},
		} {
			_, err := accept(t, e, live, r)
			wantCode(t, name, err, codes.FailedPrecondition, "plans.contract.consent_required")
		}
		_, err := accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "grace", Source: "hosted", Items: items, WithdrawalWaiverAcknowledged: true})
		wantCode(t, "the waiver without the request", err, codes.InvalidArgument, "plans.request.invalid")

		got, err := accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{CustomerExternalId: "grace", Source: "hosted", ContextRef: "inv-1", Ip: "198.51.100.4", UserAgent: "Mozilla/5.0", Locale: "en", Items: items,
			ImmediateStartConsent: true, ImmediateStartText: "I ask you to start the service now", WithdrawalWaiverAcknowledged: true, WithdrawalWaiverText: "I lose my right of withdrawal when it is fully performed",
			Order: &plansv1.ContractOrder{Description: "Basic", Total: "30.00", Currency: "USD"}})
		if err != nil || len(got.GetAcceptances()) != 2 {
			t.Fatalf("accept with consents: %v %v", got, err)
		}
		a := got.GetAcceptances()[0]
		if a.GetKind() != "distance_sales" || !a.GetObserved()["ip"] || !a.GetObserved()["text"] || a.GetContextKind() != "hosted_invoice" || !a.GetImmediateStartConsent() || !a.GetWithdrawalWaiverAcknowledged() ||
			a.GetImmediateStartText() == "" || a.GetGroupId() != got.GetAcceptances()[1].GetGroupId() || a.GetSnapshotSha256() != got.GetAcceptances()[1].GetSnapshotSha256() {
			t.Fatalf("hosted evidence: %+v", a)
		}
		if after, _ := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{CustomerExternalId: "grace"}); !after.GetSatisfied() {
			t.Error("accepted with the consents: satisfied")
		}
		if _, err := e.plans.CreateSubscription(ctx, &plansv1.CreateSubscriptionRequest{CustomerExternalId: "grace", PlanCode: "basic"}); err != nil {
			t.Errorf("subscription after the hosted acceptance: %v", err)
		}
	})

	t.Run("the snapshot is the page that was hashed, and has no personal data that will be erased", func(t *testing.T) {
		list, err := c.ListContractAcceptances(ctx, &plansv1.ListContractAcceptancesRequest{CustomerExternalId: "grace"})
		if err != nil || len(list.GetAcceptances()) != 2 {
			t.Fatalf("list: %v %v", list, err)
		}
		a := list.GetAcceptances()[0]
		snap, err := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: a.GetId()})
		if err != nil || snap.GetSha256() != a.GetSnapshotSha256() {
			t.Fatalf("the snapshot hash is the one stored with the acceptance: %v %v", snap.GetSha256(), err)
		}
		for _, want := range []string{"Distance sales for Grace Hopper: Basic 30.00 USD", "I accept the terms", "I ask you to start the service now", "I lose my right of withdrawal"} {
			if !strings.Contains(snap.GetHtml(), want) {
				t.Errorf("snapshot lacks %q", want)
			}
		}
		if strings.Contains(snap.GetHtml(), "198.51.100.4") || strings.Contains(snap.GetHtml(), "Mozilla") {
			t.Error("the snapshot carries the IP address or the user agent, which are erased later")
		}
		// Whichever acceptance of the group is asked for, the same page.
		other, _ := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: list.GetAcceptances()[1].GetId()})
		if other.GetSha256() != snap.GetSha256() {
			t.Error("two acceptances of one group gave two pages")
		}
		_, err = c.GetContractSnapshot(as(e.account(t, "Snap Other")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()), &plansv1.GetContractSnapshotRequest{AcceptanceId: a.GetId()})
		wantCode(t, "another tenant's snapshot", err, codes.NotFound, "plans.contract.acceptance_not_found")
	})

	t.Run("the PDF copy is drawn from the stored page, only when asked, and is the same every time", func(t *testing.T) {
		list, err := c.ListContractAcceptances(ctx, &plansv1.ListContractAcceptancesRequest{CustomerExternalId: "grace"})
		if err != nil || len(list.GetAcceptances()) == 0 {
			t.Fatalf("list: %v %v", list, err)
		}
		id := list.GetAcceptances()[0].GetId()
		plain, err := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: id})
		if err != nil || len(plain.GetPdf()) != 0 || plain.GetPdfUnavailable() {
			t.Fatalf("a PDF nobody asked for: %v %d %v", err, len(plain.GetPdf()), plain.GetPdfUnavailable())
		}
		withPdf, err := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: id, IncludePdf: true})
		if err != nil || !bytes.HasPrefix(withPdf.GetPdf(), []byte("%PDF-")) || withPdf.GetPdfUnavailable() || withPdf.GetSha256() != plain.GetSha256() || withPdf.GetHtml() != plain.GetHtml() {
			t.Fatalf("with the PDF: %v %v", err, withPdf.GetPdfUnavailable())
		}
		again, _ := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: id, IncludePdf: true})
		if !bytes.Equal(again.GetPdf(), withPdf.GetPdf()) {
			t.Error("the same acceptance gave two different PDFs")
		}
	})

	t.Run("a guest payment is covered by the invoice it was accepted for", func(t *testing.T) {
		g := &plansv1.GetContractRequirementsRequest{ContextRef: "inv-guest", GuestName: "Visitor", Locale: "en"}
		before, err := c.GetContractRequirements(ctx, g)
		if err != nil || before.GetSatisfied() {
			t.Fatalf("guest requirements: %v %v", before, err)
		}
		items := make([]*plansv1.ContractAcceptanceItem, 0, 2)
		for _, r := range before.GetRequirements() {
			items = append(items, &plansv1.ContractAcceptanceItem{VersionId: r.GetVersionId(), CheckboxText: "ok " + r.GetKind()})
		}
		_, err = accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{Source: "hosted", Items: items, ContextRef: "inv-guest", GuestName: "Visitor", ImmediateStartConsent: true, ImmediateStartText: "now", WithdrawalWaiverAcknowledged: true, WithdrawalWaiverText: "waived"})
		if err != nil {
			t.Fatal(err)
		}
		if after, _ := c.GetContractRequirements(ctx, g); !after.GetSatisfied() {
			t.Error("the guest accepted for this invoice")
		}
		if other, _ := c.GetContractRequirements(ctx, &plansv1.GetContractRequirementsRequest{ContextRef: "inv-other"}); other.GetSatisfied() {
			t.Error("an acceptance for one invoice covered another")
		}
		_, err = accept(t, e, live, &plansv1.RecordContractAcceptanceRequest{Source: "hosted", Items: items})
		wantCode(t, "a guest without an invoice", err, codes.InvalidArgument, "plans.request.invalid")
	})

	t.Run("acceptances are append-only; the erasure job blanks the IP address and nothing else", func(t *testing.T) {
		var id string
		if err := e.scan(context.Background(), live, `SELECT id::text FROM contract_acceptances WHERE ip <> '' ORDER BY accepted_at LIMIT 1`, nil, &id); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{`UPDATE contract_acceptances SET ip = '' WHERE id = '` + id + `'`, `DELETE FROM contract_acceptances WHERE id = '` + id + `'`,
			`UPDATE contract_acceptances SET checkbox_text = 'x'`} {
			err := pgxx.InTenantTx(context.Background(), e.appDB, uuid.MustParse(live), func(tx pgx.Tx) error { _, err := tx.Exec(context.Background(), stmt); return err })
			if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
				t.Errorf("%s as the application role: want 42501, got %v", stmt, err)
			}
		}
		// The system role may update only ip and user_agent (a column grant) ...
		err := e.exec(context.Background(), live, `UPDATE contract_acceptances SET checkbox_text = 'x' WHERE id = $1`, id)
		if pgxx.ErrCode(err) != pgxx.CodeInsufficientPriv {
			t.Errorf("the system role changed the words the customer saw: %v", err)
		}
		// ... and only to blank them; a live tenant's rows are not deleted at all.
		err = e.exec(context.Background(), live, `UPDATE contract_acceptances SET ip = '10.0.0.1' WHERE id = $1`, id)
		if pgxx.ErrCode(err) != pgxx.CodeRaiseException {
			t.Errorf("the system role rewrote the IP address: %v", err)
		}
		err = e.exec(context.Background(), live, `DELETE FROM contract_acceptances WHERE id = $1`, id)
		if pgxx.ErrCode(err) != pgxx.CodeRaiseException {
			t.Errorf("the system role deleted evidence of a live tenant: %v", err)
		}
		before, _ := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: id})

		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		young := contracts.NewEraser(e.places, 0, log).WithClock(func() time.Time { return time.Now().AddDate(2, 0, 0) })
		if _, err := young.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
		var ip string
		if err := e.scan(context.Background(), live, `SELECT ip FROM contract_acceptances WHERE id = $1`, []any{id}, &ip); err != nil || ip == "" {
			t.Fatalf("two years on, inside 36 months, the IP address was blanked: %q %v", ip, err)
		}
		old := contracts.NewEraser(e.places, 0, log).WithClock(func() time.Time { return time.Now().AddDate(4, 0, 0) })
		if n, err := old.Once(context.Background()); err != nil || n == 0 {
			t.Fatalf("four years on: %d %v", n, err)
		}
		var ua, checkbox string
		if err := e.scan(context.Background(), live, `SELECT ip, user_agent, checkbox_text FROM contract_acceptances WHERE id = $1`, []any{id}, &ip, &ua, &checkbox); err != nil {
			t.Fatal(err)
		}
		if ip != "" || ua != "" || checkbox == "" {
			t.Errorf("after the erasure: ip %q, user agent %q, checkbox %q", ip, ua, checkbox)
		}
		after, err := c.GetContractSnapshot(ctx, &plansv1.GetContractSnapshotRequest{AcceptanceId: id})
		if err != nil || after.GetSha256() != before.GetSha256() {
			t.Errorf("the snapshot changed with the erasure: %v", err)
		}
		stResp, _ := c.SetContractSettings(ctx, &plansv1.SetContractSettingsRequest{RequiredKinds: []string{"distance_sales", "tos"}, ImmediateStart: true, RetentionMonths: 12})
		if st := stResp.GetSettings(); st.GetRetentionMonths() != 12 {
			t.Errorf("retention: %v", st)
		}
	})

	t.Run("withdrawal requests are recorded, decided once and announced", func(t *testing.T) {
		wResp, err := c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "grace", Statement: "I changed my mind", Source: "api"})
		w := wResp.GetWithdrawal()
		if err != nil || w.GetStatus() != "received" || w.GetCustomerExternalId() != "grace" || w.GetDecideTime() != nil {
			t.Fatalf("withdrawal: %v %v", w, err)
		}
		_, err = c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "nobody"})
		wantCode(t, "unknown customer", err, codes.NotFound, "")
		_, err = c.CreateContractWithdrawal(ctx, &plansv1.CreateContractWithdrawalRequest{CustomerExternalId: "grace", InvoiceId: "not-a-uuid"})
		wantCode(t, "bad invoice id", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w.GetId(), Status: "refunded"})
		wantCode(t, "refunded before accepted", err, codes.FailedPrecondition, "plans.contract.withdrawal_decided")
		dResp, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w.GetId(), Status: "accepted", Note: "within 14 days", DecidedBy: "owner@example.com"})
		d := dResp.GetWithdrawal()
		if err != nil || d.GetStatus() != "accepted" || d.GetDecideTime() == nil || d.GetDecidedBy() != "owner@example.com" {
			t.Fatalf("decide: %v %v", d, err)
		}
		_, err = c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w.GetId(), Status: "rejected"})
		wantCode(t, "accepted becomes rejected", err, codes.FailedPrecondition, "plans.contract.withdrawal_decided")
		if _, err := c.SetContractWithdrawalStatus(ctx, &plansv1.SetContractWithdrawalStatusRequest{Id: w.GetId(), Status: "refunded"}); err != nil {
			t.Errorf("accepted becomes refunded: %v", err)
		}
		l, err := c.ListContractWithdrawals(ctx, &plansv1.ListContractWithdrawalsRequest{CustomerExternalId: "grace", Status: "refunded"})
		if err != nil || len(l.GetWithdrawals()) != 1 {
			t.Errorf("list: %v %v", l, err)
		}
	})

	t.Run("events: webhooks for version, acceptance and withdrawal; the mail fact for a customer, none for a guest", func(t *testing.T) {
		for _, typ := range []string{"contract.version_published", "contract.accepted", "contract.withdrawal_requested"} {
			var n int
			if err := e.scan(context.Background(), live, `SELECT count(*) FROM webhook_events WHERE tenant_id = $2 AND type = $1`, []any{typ, uuid.MustParse(live)}, &n); err != nil || n == 0 {
				t.Errorf("no %s event: %d %v", typ, n, err)
			}
		}
		var facts int
		if err := e.scan(context.Background(), live, `SELECT count(*) FROM outbox WHERE tenant_id = $1 AND topic = 'contract.accepted.v1'`, []any{uuid.MustParse(live)}, &facts); err != nil || facts != 2 {
			t.Errorf("one fact for each acceptance group of a customer (ada by the API, grace on a page); the guest has none: %d %v", facts, err)
		}
	})

	t.Run("starter texts and preview", func(t *testing.T) {
		for _, kind := range domain.ContractKinds {
			for _, loc := range []string{"tr", "en"} {
				s, err := c.GetContractStarter(ctx, &plansv1.GetContractStarterRequest{Kind: kind, Locale: loc})
				if err != nil || !domain.HasTemplateNotice(s.GetBody()) {
					t.Errorf("starter %s/%s: %v", kind, loc, err)
				}
			}
		}
		_, err := c.GetContractStarter(ctx, &plansv1.GetContractStarterRequest{Kind: "tos", Locale: "de"})
		wantCode(t, "no machine-translated starter", err, codes.NotFound, "plans.contract.no_starter")
		p, err := c.PreviewContract(ctx, &plansv1.PreviewContractRequest{Body: "Hi {{customer_name}} <b>{{oops}}</b>"})
		if err != nil || len(p.GetUnknownVariables()) != 1 || strings.Contains(p.GetHtml(), "<b>") || !strings.Contains(p.GetHtml(), "<mark>Ada Lovelace</mark>") {
			t.Errorf("preview: %v %v", p, err)
		}
		v := publishContract(t, e, live, "privacy", "", true, map[string]string{"tr": mustStarter(t, "privacy", "tr")})
		if !v.GetHasTemplateNotice() {
			t.Error("a published starter text still carries the notice, and the version says so")
		}
	})
}

func mustStarter(t *testing.T, kind, loc string) string {
	t.Helper()
	body, _, ok := contracts.Starter(kind, loc)
	if !ok {
		t.Fatalf("no starter %s/%s", kind, loc)
	}
	return body
}
