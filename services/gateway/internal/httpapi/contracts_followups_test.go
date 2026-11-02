package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// --- what the gateway sees of billing's credit notes and of plans-svc's follow-up calls ---

var (
	cnMu       sync.Mutex
	cnCreated  []*billingv1.CreateCreditNoteRequest
	cnOperator []bool
	cnTenant   []string
	cnErr      error
	cnExisting []*billingv1.CreditNote
)

func resetCreditNotes() {
	cnMu.Lock()
	cnCreated, cnOperator, cnTenant, cnErr, cnExisting = nil, nil, nil, nil, nil
	cnMu.Unlock()
}

func (fakeBilling) CreateCreditNote(ctx context.Context, req *billingv1.CreateCreditNoteRequest) (*billingv1.CreateCreditNoteResponse, error) {
	cnMu.Lock()
	defer cnMu.Unlock()
	ti, _ := tenant.FromContext(ctx)
	cnCreated, cnOperator, cnTenant = append(cnCreated, req), append(cnOperator, tenant.IsOperator(ctx)), append(cnTenant, ti.ID.String())
	if cnErr != nil {
		return nil, cnErr
	}
	return &billingv1.CreateCreditNoteResponse{CreditNote: &billingv1.CreditNote{Id: "cn-1", InvoiceId: req.GetInvoiceId(), Reason: req.GetReason()}}, nil
}

func (fakeBilling) ListCreditNotes(context.Context, *billingv1.ListCreditNotesRequest) (*billingv1.ListCreditNotesResponse, error) {
	cnMu.Lock()
	defer cnMu.Unlock()
	return &billingv1.ListCreditNotesResponse{CreditNotes: cnExisting}, nil
}

func (f *fakeContracts) GetContractSettings(context.Context, *plansv1.GetContractSettingsRequest) (*plansv1.GetContractSettingsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settings == nil {
		return &plansv1.GetContractSettingsResponse{Settings: &plansv1.ContractSettings{RequiredKinds: []string{"tos"}, WithdrawalRefund: "full", WithdrawalFunction: true, RenewalNoticeDays: 7}}, nil
	}
	return &plansv1.GetContractSettingsResponse{Settings: f.settings}, nil
}

func (f *fakeContracts) ListContractAcceptances(_ context.Context, req *plansv1.ListContractAcceptancesRequest) (*plansv1.ListContractAcceptancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = req
	return &plansv1.ListContractAcceptancesResponse{Acceptances: f.accepted, NextPageToken: f.nextPage}, nil
}

func (f *fakeContracts) ListContractWithdrawals(context.Context, *plansv1.ListContractWithdrawalsRequest) (*plansv1.ListContractWithdrawalsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &plansv1.ListContractWithdrawalsResponse{Withdrawals: f.withdrawals}, nil
}

func (f *fakeContracts) ClaimContractWithdrawalRefund(_ context.Context, req *plansv1.ClaimContractWithdrawalRefundRequest) (*plansv1.ClaimContractWithdrawalRefundResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, req.GetId())
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	return &plansv1.ClaimContractWithdrawalRefundResponse{Reclaimed: f.reclaimed, Withdrawal: &plansv1.ContractWithdrawal{Id: req.GetId(), InvoiceId: "inv-paid", Status: "accepted", RefundState: "requesting"}}, nil
}

func (f *fakeContracts) RecordContractWithdrawalRefund(_ context.Context, req *plansv1.RecordContractWithdrawalRefundRequest) (*plansv1.RecordContractWithdrawalRefundResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds = append(f.refunds, req)
	return &plansv1.RecordContractWithdrawalRefundResponse{Withdrawal: &plansv1.ContractWithdrawal{Id: req.GetId(), InvoiceId: "inv-paid", Status: "accepted", RefundState: req.GetState(), CreditNoteId: req.GetCreditNoteId(), RefundNote: req.GetNote()}}, nil
}

func lastRefund(c *fakeContracts) *plansv1.RecordContractWithdrawalRefundRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.refunds) == 0 {
		return nil
	}
	return c.refunds[len(c.refunds)-1]
}

// --- routing a withdrawal to a refund ---

func TestAcceptingAWithdrawalAsksBillingForTheRefundThroughTheCreditNotePath(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts
	resetCreditNotes()
	c.decideInvoice = "inv-paid"

	r, m := do(t, ts, http.MethodPut, "/v1/contract-withdrawals/wd-1/status", "billing-key", `{"status":"accepted","note":"within 14 days","refund_amount_minor":"1500"}`)
	if r.StatusCode != http.StatusOK || m["refund_state"] != "requested" || m["credit_note_id"] != "cn-1" {
		t.Fatalf("accept: %d %v", r.StatusCode, m)
	}
	cnMu.Lock()
	defer cnMu.Unlock()
	if len(cnCreated) != 1 {
		t.Fatalf("credit notes asked for: %d", len(cnCreated))
	}
	req := cnCreated[0]
	// A credit note that returns the money to the card, for the amount the seller named, on the withdrawal's invoice, as the tenant's own call; it is the gateway that is
	// the operator here (only billing's own path makes the note), and the reason names the withdrawal so that a lost answer can be found again.
	if !req.GetRefundToCard() || req.GetAmountMinor() != 1500 || req.GetInvoiceId() != "inv-paid" || req.GetTenantId() != cnTenant[0] || !cnOperator[0] || req.GetReason() != "withdrawal wd-1" {
		t.Errorf("credit note request = %+v operator=%v tenant=%v", req, cnOperator[0], cnTenant[0])
	}
	if rec := lastRefund(c); rec.GetState() != "requested" || rec.GetCreditNoteId() != "cn-1" {
		t.Errorf("recorded = %+v", rec)
	}
}

func TestTheSettingNoneLeavesTheRefundToTheSellerUntilItAsks(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts
	resetCreditNotes()
	c.decideInvoice = "inv-paid"
	c.settings = &plansv1.ContractSettings{RequiredKinds: []string{"tos"}, WithdrawalRefund: "none", WithdrawalFunction: true}

	if r, m := do(t, ts, http.MethodPut, "/v1/contract-withdrawals/wd-1/status", "billing-key", `{"status":"accepted"}`); r.StatusCode != http.StatusOK || m["status"] != "accepted" {
		t.Fatalf("accept: %d %v", r.StatusCode, m)
	}
	cnMu.Lock()
	n := len(cnCreated)
	cnMu.Unlock()
	if n != 0 || len(c.claims) != 0 {
		t.Fatalf("nothing is asked of billing unless the settings or the seller say so: %d credit notes, %d claims", n, len(c.claims))
	}
	// A rejection never refunds, whatever the setting.
	c.settings = nil
	if r, _ := do(t, ts, http.MethodPut, "/v1/contract-withdrawals/wd-1/status", "billing-key", `{"status":"rejected"}`); r.StatusCode != http.StatusOK {
		t.Fatal(r.StatusCode)
	}
	cnMu.Lock()
	n = len(cnCreated)
	cnMu.Unlock()
	if n != 0 {
		t.Errorf("a rejected withdrawal asked billing for a refund")
	}
	// The seller asks for it: the refund endpoint.
	r, m := do(t, ts, http.MethodPost, "/v1/contract-withdrawals/wd-1/refund", "billing-key", `{"amount_minor":"700"}`)
	cnMu.Lock()
	defer cnMu.Unlock()
	if r.StatusCode != http.StatusOK || m["refund_state"] != "requested" || len(cnCreated) != 1 || cnCreated[0].GetAmountMinor() != 700 {
		t.Fatalf("refund: %d %v %v", r.StatusCode, m, cnCreated)
	}
}

func TestAWithdrawalPaidOutsidePayrudderIsMarkedAndADecisionNeverFailsOnTheRefund(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts
	c.decideInvoice = "inv-paid"
	for _, tc := range []struct {
		name, state, note string
		err               error
	}{
		{"a bank transfer", "outside", "outside Payrudder", errkey.New(codes.FailedPrecondition, "billing.credit_note.nothing_to_refund", "no card payment")},
		{"not paid", "none", "not paid", errkey.New(codes.FailedPrecondition, "billing.credit_note.not_paid", "not paid")},
		{"credited already", "none", "credited in full", errkey.New(codes.FailedPrecondition, "billing.credit_note.fully_credited", "done")},
		{"billing is down", "failed", "", status.Error(codes.Unavailable, "billing is down")},
	} {
		resetCreditNotes()
		cnMu.Lock()
		cnErr = tc.err
		cnMu.Unlock()
		r, m := do(t, ts, http.MethodPut, "/v1/contract-withdrawals/wd-1/status", "billing-key", `{"status":"accepted"}`)
		if r.StatusCode != http.StatusOK || m["status"] != "accepted" || m["refund_state"] != tc.state {
			t.Errorf("%s: %d %v: the decision stands and the withdrawal says what happened to the refund", tc.name, r.StatusCode, m)
		}
		if note, _ := m["refund_note"].(string); !strings.Contains(note, tc.note) || (tc.state == "failed" && note == "") {
			t.Errorf("%s: note %q", tc.name, note)
		}
		if rec := lastRefund(c); rec.GetCreditNoteId() != "" {
			t.Errorf("%s: no credit note to link: %+v", tc.name, rec)
		}
	}
}

func TestARefundIsNeverAskedForTwiceAndALostAnswerIsFoundAgain(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts
	resetCreditNotes()
	c.claimErr = errkey.New(codes.FailedPrecondition, "plans.contract.refund_not_possible", "a refund was asked for already")
	r, m := do(t, ts, http.MethodPost, "/v1/contract-withdrawals/wd-1/refund", "billing-key", ``)
	cnMu.Lock()
	n := len(cnCreated)
	cnMu.Unlock()
	if r.StatusCode != http.StatusConflict && r.StatusCode != http.StatusPreconditionFailed && r.StatusCode != http.StatusUnprocessableEntity || n != 0 {
		t.Fatalf("a claim plans-svc refuses stops here: %d %v, %d credit notes", r.StatusCode, m, n)
	}
	// A claim taken over from a caller that died: the credit note it made is found by its reason, and no second one is made.
	c.claimErr, c.reclaimed = nil, true
	cnMu.Lock()
	cnExisting = []*billingv1.CreditNote{{Id: "cn-other", Reason: "withdrawal wd-9"}, {Id: "cn-earlier", Reason: "withdrawal wd-1"}}
	cnMu.Unlock()
	r, m = do(t, ts, http.MethodPost, "/v1/contract-withdrawals/wd-1/refund", "billing-key", ``)
	cnMu.Lock()
	defer cnMu.Unlock()
	if r.StatusCode != http.StatusOK || m["credit_note_id"] != "cn-earlier" || m["refund_state"] != "requested" || len(cnCreated) != 0 {
		t.Fatalf("reclaimed: %d %v, %d new credit notes", r.StatusCode, m, len(cnCreated))
	}
}

func TestTheRefundRouteNeedsAWriteKey(t *testing.T) {
	ts, _ := newTestServer(t)
	if r, _ := do(t, ts, http.MethodPost, "/v1/contract-withdrawals/wd-1/refund", "read-key", ``); r.StatusCode != http.StatusForbidden {
		t.Errorf("a read-only key asked for a refund: %d", r.StatusCode)
	}
}

// --- the withdrawal function of the hosted pages ---

// paidCustomerServer is a gateway whose seller requires agreements and whose customer ada is an individual with an address.
func paidCustomerServer(t *testing.T) (*httptest.Server, *fakeContracts, *fakePlans) {
	t.Helper()
	ts, plans := newTestServer(t)
	plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", Name: "Ada Lovelace", Email: "ada@example.com", CustomerType: "individual", Locale: "en"}
	return ts, lastContracts, plans
}

func TestTheWithdrawalFunctionIsLinkedFromTheInvoiceAndThePortalAndWorksInTwoSteps(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)

	// The links: "Withdraw from contract here" on the paid invoice and on the portal.
	if _, page := get(t, ts, "/i/"+tokPaidCustomer); !strings.Contains(page, `href="/i/`+tokPaidCustomer+`/withdraw"`) || !strings.Contains(page, "Withdraw from contract here") {
		t.Errorf("the paid invoice does not offer the function:\n%s", page)
	}
	if _, page := get(t, ts, "/c/"+portalTok); !strings.Contains(page, `href="/c/`+portalTok+`/withdraw"`) {
		t.Errorf("the portal does not offer the function:\n%s", page)
	}
	if _, page := get(t, ts, "/i/"+tokOpenSandbox); strings.Contains(page, "/withdraw") {
		t.Error("an invoice that is not paid has nothing to withdraw from")
	}

	// Step 1: the form asks for the name (prefilled), shows the contract and where the confirmation goes (masked), and sends nothing.
	resp, form := get(t, ts, "/i/"+tokPaidCustomer+"/withdraw")
	for _, want := range []string{"Withdraw from contract here", `name="name" value="Ada Lovelace"`, "Invoice INV-2026-000009", `name="contract" value="inv-open"`, "a***@example.com", `action="/i/` + tokPaidCustomer + `/withdraw"`} {
		if !strings.Contains(form, want) {
			t.Errorf("the form lacks %q:\n%s", want, form)
		}
	}
	if strings.Contains(form, "ada@example.com") || resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "form-action 'self'") {
		t.Errorf("the address is shown whole, or the page is cacheable, or the policy lets the form go elsewhere: %v", resp.Header)
	}
	if c.withdrawal != nil {
		t.Fatal("opening the form recorded a withdrawal")
	}

	// Step 2: review. Nothing is recorded; the button is the confirmation function and nothing else.
	post := func(path string, f url.Values) (*http.Response, string) {
		return send(t, noRedirect(), http.MethodPost, ts.URL+path, f, nil)
	}
	fields := url.Values{"name": {"Ada Lovelace"}, "contract": {"inv-open"}, "words": {"I changed my mind <b>now</b>"}}
	resp, review := post("/i/"+tokPaidCustomer+"/withdraw", fields)
	if resp.StatusCode != http.StatusOK || !strings.Contains(review, "Confirm withdrawal") || !strings.Contains(review, `action="/i/`+tokPaidCustomer+`/withdraw/confirm"`) || strings.Contains(review, "<b>now</b>") {
		t.Fatalf("review: %d\n%s", resp.StatusCode, review)
	}
	if c.withdrawal != nil {
		t.Fatal("the review step recorded a withdrawal: nothing is sent until the confirmation")
	}
	if resp, again := post("/i/"+tokPaidCustomer+"/withdraw", url.Values{"name": {" "}, "contract": {"inv-open"}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(again, "Please give your name") {
		t.Errorf("no name: %d", resp.StatusCode)
	}
	if resp, _ := post("/i/"+tokPaidCustomer+"/withdraw/confirm", url.Values{"name": {"Ada"}, "contract": {"an-invoice-of-someone-else"}}); resp.StatusCode != http.StatusUnprocessableEntity || c.withdrawal != nil {
		t.Errorf("a contract the page did not offer was recorded: %d %+v", resp.StatusCode, c.withdrawal)
	}

	// Step 3: confirm. One withdrawal from the hosted page, with what the customer confirmed, for the invoice, in the tenant of the invoice.
	resp, done := post("/i/"+tokPaidCustomer+"/withdraw/confirm", fields)
	w := c.withdrawal
	if resp.StatusCode != http.StatusOK || w == nil || w.GetSource() != "hosted" || w.GetCustomerId() != "cust-uuid" || w.GetInvoiceId() != "inv-open" {
		t.Fatalf("confirm: %d %+v\n%s", resp.StatusCode, w, done)
	}
	for _, want := range []string{"Your name: Ada Lovelace", "Invoice INV-2026-000009", "E-mail for the confirmation: ada@example.com", "I changed my mind <b>now</b>"} {
		if !strings.Contains(w.GetStatement(), want) {
			t.Errorf("the statement lacks %q: %q", want, w.GetStatement())
		}
	}
	if !strings.Contains(done, "Withdrawal received") || !strings.Contains(done, "acknowledgement of receipt") || !strings.Contains(done, "A copy was sent to your e-mail address.") || strings.Contains(done, "<b>now</b>") {
		t.Errorf("the acknowledgement page:\n%s", done)
	}

	// Pressing the button again, or a second tab, records nothing more.
	c.withdrawal = nil
	c.withdrawals = []*plansv1.ContractWithdrawal{{Id: "wd-1", InvoiceId: "inv-open", Status: "received"}}
	if resp, again := post("/i/"+tokPaidCustomer+"/withdraw/confirm", fields); resp.StatusCode != http.StatusOK || c.withdrawal != nil || !strings.Contains(again, "received already") {
		t.Errorf("a second confirmation: %d %+v\n%s", resp.StatusCode, c.withdrawal, again)
	}
}

// plans-svc keeps 2000 bytes of a statement: a customer who writes a long text in Arabic (two bytes a character) has it kept as far as it goes, never refused.
func TestALongStatementInAWideScriptIsCutAtACharacterAndNotRefused(t *testing.T) {
	for _, lang := range hostedLangs {
		l := hostedText(lang)
		got := statementOf(l, strings.Repeat("ع", 200), "Invoice INV-1", "ada@example.com", strings.Repeat("ç", 1000)+strings.Repeat("ع", 600))
		if len(got) > maxStatement || !utf8.ValidString(got) || !strings.Contains(got, l["withdraw_words"]+": ç") {
			t.Errorf("%s: %d bytes, valid=%v", lang, len(got), utf8.ValidString(got))
		}
	}
	short := statementOf(hostedText("en"), "Ada", "Invoice 1", "", "")
	if short != "Your name: Ada\nContract you withdraw from: Invoice 1" {
		t.Errorf("a statement without words or address: %q", short)
	}
}

func TestTheWithdrawalFunctionIsOfferedToAConsumerOfASellerThatAsksForAgreementsOnly(t *testing.T) {
	ts, c, plans := paidCustomerServer(t)
	for _, tc := range []struct {
		name  string
		setup func()
	}{
		{"the seller requires no agreements", func() {
			c.settings = &plansv1.ContractSettings{WithdrawalFunction: true, WithdrawalRefund: "full"}
		}},
		{"the seller switched the function off", func() {
			c.settings = &plansv1.ContractSettings{RequiredKinds: []string{"tos"}, WithdrawalFunction: false}
		}},
		{"a business customer", func() {
			plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", Name: "Acme", Email: "ap@acme.example", CustomerType: "business"}
		}},
	} {
		c.settings = nil
		plans.customer = &plansv1.Customer{Id: "cust-uuid", ExternalId: "ada-ext", Name: "Ada", Email: "ada@example.com", CustomerType: "individual"}
		tc.setup()
		if _, page := get(t, ts, "/i/"+tokPaidCustomer); strings.Contains(page, "/withdraw") {
			t.Errorf("%s: the invoice page links to the function", tc.name)
		}
		if resp, _ := get(t, ts, "/i/"+tokPaidCustomer+"/withdraw"); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: the form opens (%d)", tc.name, resp.StatusCode)
		}
		if resp, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokPaidCustomer+"/withdraw/confirm", url.Values{"name": {"Ada"}, "contract": {"inv-open"}}, nil); resp.StatusCode != http.StatusNotFound || c.withdrawal != nil {
			t.Errorf("%s: a withdrawal was recorded (%d)", tc.name, resp.StatusCode)
		}
	}
	c.settings, plans.customer = nil, &plansv1.Customer{Id: "cust-uuid", Name: "Ada", Email: "ada@example.com"}
	if resp, _ := get(t, ts, "/i/"+tokPaidGuest+"/withdraw"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a guest's payment link has no customer to withdraw: %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts, "/i/"+tokOpenSandbox+"/withdraw"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unpaid invoice: %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts, "/i/no-such-token/withdraw"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown link: %d", resp.StatusCode)
	}
}

func TestThePortalOffersTheCustomersPaidInvoicesToWithdrawFrom(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)
	_, form := get(t, ts, "/c/"+portalTok+"/withdraw")
	// Only the paid invoice of the portal (inv-paid), never the open one, and only a contract the customer can name.
	if !strings.Contains(form, "INV-2026-000003") || strings.Contains(form, "INV-2026-000009") || !strings.Contains(form, `name="contract" value="inv-paid"`) {
		t.Fatalf("the portal form:\n%s", form)
	}
	resp, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/withdraw/confirm", url.Values{"name": {"Ada"}, "contract": {"inv-open"}}, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || c.withdrawal != nil {
		t.Errorf("an unpaid invoice of the portal was withdrawn from: %d", resp.StatusCode)
	}
	resp, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/withdraw/confirm", url.Values{"name": {"Ada"}, "contract": {"inv-paid"}}, nil)
	if resp.StatusCode != http.StatusOK || c.withdrawal.GetInvoiceId() != "inv-paid" || c.withdrawal.GetCustomerId() != "cust-uuid" || c.withdrawal.GetSource() != "hosted" {
		t.Errorf("portal confirm: %d %+v", resp.StatusCode, c.withdrawal)
	}
}

// --- the agreements on the portal ---

func acceptedBy(id, kind string, n int32) *plansv1.ContractAcceptance {
	return &plansv1.ContractAcceptance{Id: id, Kind: kind, VersionNumber: n, AcceptTime: timestamppb.New(mustDay(2026, 9, 2)), Ip: "203.0.113.9", UserAgent: "ua"}
}

func TestThePortalListsWhatTheCustomerAcceptedWithTheStoredPage(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)
	c.accepted = []*plansv1.ContractAcceptance{acceptedBy("acc-1", "tos", 2), acceptedBy("acc-0", "tos", 1), acceptedBy("acc-9", "privacy", 1)}
	_, page := get(t, ts, "/c/"+portalTok)
	for _, want := range []string{"What you accepted", "Terms of service", "Privacy notice", `href="/c/` + portalTok + `/agreements/acc-1"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the portal lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "acc-0") || strings.Contains(page, "203.0.113.9") {
		t.Error("an older version is listed, or the address of the acceptance is shown")
	}
	resp, snap := get(t, ts, "/c/"+portalTok+"/agreements/acc-1")
	if resp.StatusCode != http.StatusOK || !strings.Contains(snap, "the page") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("the stored page: %d %v\n%s", resp.StatusCode, resp.Header, snap)
	}
	// An acceptance that is not this customer's is a 404, even when it exists (the fake knows acc-1 only).
	if resp, _ := get(t, ts, "/c/"+portalTok+"/agreements/acc-someone-else"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("another customer's acceptance: %d", resp.StatusCode)
	}
	if resp, _ := get(t, ts, "/c/no-such-portal/agreements/acc-1"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown portal: %d", resp.StatusCode)
	}
}

func TestThePortalAsksAgainWhenAMaterialVersionWasPublishedAndOnlyOfSomeoneWhoAcceptedBefore(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)

	c.enforce(false)
	if _, page := get(t, ts, "/c/"+portalTok); strings.Contains(page, `class="agree"`) {
		t.Fatal("a customer who never accepted anything is asked on the portal: their first acceptance is where they pay")
	}
	c.accepted = []*plansv1.ContractAcceptance{acceptedBy("acc-1", "distance_sales", 2)}
	_, page := get(t, ts, "/c/"+portalTok)
	for _, want := range []string{`class="agree"`, "These agreements changed since you last accepted them", "Distance sales text", `action="/c/` + portalTok + `/agreements"`, `name="v_tos" value="ver-tos"`, `name="accept_tos" value="1" required`} {
		if !strings.Contains(page, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "Order with obligation to pay") {
		t.Error("nothing is bought on the portal: the button only accepts")
	}
	if last := c.asked[len(c.asked)-1]; last.GetCustomerId() != "cust-uuid" || last.GetLocale() != "en" {
		t.Errorf("asked about %+v", last)
	}

	// A box left empty, or a text replaced since the page was drawn, records nothing and shows the portal again.
	post := func(f url.Values) (*http.Response, string) {
		return send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/agreements", f, nil)
	}
	n := len(c.recorded)
	if resp, again := post(url.Values{"v_distance_sales": {"ver-ds"}, "v_tos": {"ver-tos"}, "accept_tos": {"1"}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(again, "Please read and tick every box") || len(c.recorded) != n {
		t.Errorf("a box missing: %d recorded=%d", resp.StatusCode, len(c.recorded)-n)
	}
	if resp, again := post(url.Values{"v_distance_sales": {"old"}, "v_tos": {"ver-tos"}, "accept_distance_sales": {"1"}, "accept_tos": {"1"}}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(again, "updated while you were reading") || len(c.recorded) != n {
		t.Errorf("a stale text: %d", resp.StatusCode)
	}

	// Complete: one acceptance from the portal, with what the server saw, and back to the portal.
	resp, _ := post(url.Values{"v_distance_sales": {"ver-ds"}, "v_tos": {"ver-tos"}, "accept_distance_sales": {"1"}, "accept_tos": {"1"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/c/"+portalTok || len(c.recorded) != n+1 {
		t.Fatalf("accept: %d %v recorded=%d", resp.StatusCode, resp.Header, len(c.recorded)-n)
	}
	rec := c.recorded[len(c.recorded)-1]
	if rec.GetSource() != "hosted" || rec.GetContextKind() != "portal" || rec.GetCustomerId() != "cust-uuid" || len(rec.GetItems()) != 2 || rec.GetUserAgent() == "" || c.recordedAs[len(c.recordedAs)-1] != liveTenantID {
		t.Errorf("recorded %+v for tenant %v", rec, c.recordedAs)
	}
	// Satisfied: the prompt is gone.
	if _, after := get(t, ts, "/c/"+portalTok); strings.Contains(after, `class="agree"`) {
		t.Error("the prompt stays after the customer accepted")
	}
}

func TestPortalAgreementsAreOptionalAndFailOpenForThePage(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)
	c.enforce(false)
	c.accepted = []*plansv1.ContractAcceptance{acceptedBy("acc-1", "tos", 1)}
	c.reqErr = status.Error(codes.Unavailable, "plans is down")
	resp, page := get(t, ts, "/c/"+portalTok)
	if resp.StatusCode != http.StatusOK || strings.Contains(page, `class="agree"`) || !strings.Contains(page, "INV-2026-000003") {
		t.Errorf("a portal whose requirements cannot be read is still the portal: %d", resp.StatusCode)
	}
	resp, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/agreements", url.Values{"accept_tos": {"1"}}, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("accepting while the requirements cannot be read: %d", resp.StatusCode)
	}
}

func mustDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
