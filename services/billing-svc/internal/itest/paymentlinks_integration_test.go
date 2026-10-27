//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/api"
)

// linkEnv is a tenant with the billing API in front of it, as the gateway calls it: the tenant's own calls carry its id, a visitor's carry nothing.
type linkEnv struct {
	*env
	svc     *api.Server
	mine    context.Context
	visitor context.Context
}

func newLinkEnv(t *testing.T, o envOpts, demo bool) *linkEnv {
	t.Helper()
	e := newEnv(t, o)
	return &linkEnv{env: e, svc: e.api().WithPDFs(fakePDFs{}).WithDemo(demo),
		mine: tenant.NewContext(context.Background(), tenant.Info{ID: e.tid}), visitor: context.Background()}
}

func (l *linkEnv) create(req *billingv1.CreatePaymentLinkRequest) *billingv1.CreatePaymentLinkResponse {
	l.t.Helper()
	r, err := l.svc.CreatePaymentLink(l.mine, req)
	if err != nil {
		l.t.Fatalf("create %v: %v", req, err)
	}
	return r
}

func (l *linkEnv) page(token string) (*billingv1.GetHostedInvoiceResponse, error) {
	return l.svc.GetHostedInvoice(l.visitor, &billingv1.GetHostedInvoiceRequest{Token: token})
}

// start presses Continue on an ad-hoc link and returns the invoice link's token.
func (l *linkEnv) start(token string) string {
	l.t.Helper()
	r, err := l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: token})
	if err != nil {
		l.t.Fatalf("start: %v", err)
	}
	return r.GetInvoiceToken()
}

func (l *linkEnv) waitState(token, state string) *billingv1.GetHostedInvoiceResponse {
	l.t.Helper()
	var last *billingv1.GetHostedInvoiceResponse
	testkit.Eventually(l.t, 30*time.Second, "payment state "+state, func() bool {
		last, _ = l.page(token)
		return last.GetPaymentState() == state
	})
	return last
}

func (l *linkEnv) link(id string) *billingv1.PaymentLink {
	l.t.Helper()
	r, err := l.svc.GetPaymentLink(l.mine, &billingv1.GetPaymentLinkRequest{LinkId: id})
	if err != nil {
		l.t.Fatal(err)
	}
	return r.GetLink()
}

// events flushes the tenant's outbox to the fake plans-svc and returns the event types handed over.
func (l *linkEnv) events() []string {
	l.t.Helper()
	for i := 0; i < 20; i++ { // the hand-over takes the oldest 200 of every tenant: earlier tests' leftovers go first
		if err := l.relay.Flush(context.Background()); err != nil {
			l.t.Fatal(err)
		}
		var left int
		if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM tenant_event_outbox WHERE tenant_id = $1`, l.tid).Scan(&left); err != nil {
			l.t.Fatal(err)
		}
		if left == 0 {
			break
		}
	}
	var out []string
	for _, r := range allEvents.recorded(l.tid) {
		out = append(out, r.GetType())
	}
	return out
}

func keyOf(t *testing.T, err error, code codes.Code, key string) {
	t.Helper()
	if status.Code(err) != code || errkey.Of(err) != key {
		t.Errorf("error = %v (code %v, key %q), want %v %q", err, status.Code(err), errkey.Of(err), code, key)
	}
}

func testAdhocPaymentLinkPaid(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	worker := l.startWorkers(nil)

	c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 4900, Description: "Onboarding workshop", IdempotencyKey: "paid-1"})
	if len(c.GetToken()) < 40 || c.GetLink().GetStatus() != "active" || !c.GetLink().GetSingleUse() || c.GetLink().GetCurrency() != "USD" || c.GetLink().GetKind() != "adhoc" {
		t.Fatalf("link = %v", c)
	}

	// The landing page is the request, not an invoice: opening it creates nothing.
	landing, err := l.page(c.GetToken())
	if err != nil || landing.GetKind() != "adhoc" || landing.GetAdhoc().GetAmountMinor() != 4900 || landing.GetInvoice() != nil || landing.GetEnvironment() != "sandbox" || landing.GetSellerName() != "Acme" {
		t.Fatalf("landing = %v, %v", landing, err)
	}
	var invoices int
	linkInvoices := func() int {
		if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1 AND kind = 'LINK'`, l.tid).Scan(&invoices); err != nil {
			t.Fatal(err)
		}
		return invoices
	}
	if linkInvoices() != 0 {
		t.Fatalf("opening the page made %d invoices", invoices)
	}
	// The landing page of an ad-hoc link cannot be paid: Continue comes first.
	if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: c.GetToken(), Method: "mock"}); status.Code(err) != codes.NotFound {
		t.Errorf("paying an ad-hoc link before Continue: %v", err)
	}

	invToken := l.start(c.GetToken())
	if linkInvoices() != 1 {
		t.Fatalf("Continue made %d invoices", invoices)
	}
	open := l.waitState(invToken, "open")
	inv := open.GetInvoice()
	if !open.GetPayable() || inv.GetKind() != "LINK" || !strings.HasPrefix(inv.GetNumber(), "SBX-") || inv.GetTotalMinor() != 4900 || inv.GetSagaState() != "AWAITING_PAYMENT" ||
		inv.GetPlanVersionId() != "" || len(inv.GetLines()) != 1 || inv.GetLines()[0].GetDescription() != "Onboarding workshop" {
		t.Fatalf("invoice page = %v", open)
	}
	if open.GetPayment().GetInvoiceId() != inv.GetId() || open.GetPayment().GetAmountMinor() != 4900 || open.GetPayment().GetTenantId() != l.tid.String() {
		t.Errorf("payment context = %v", open.GetPayment())
	}
	// Nothing is charged until the customer pays.
	time.Sleep(500 * time.Millisecond)
	if n := chargesOf(worker, inv.GetId()); n != 0 {
		t.Fatalf("%d charges before the customer paid", n)
	}

	// Pay with the mock. The saga charges; the page is processing, then paid, driven by the saga.
	r, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: invToken, Method: "mock"})
	if err != nil || r.GetAction() != "started" {
		t.Fatalf("pay = %v, %v", r, err)
	}
	paid := l.waitState(invToken, "paid")
	if paid.GetPayable() || paid.GetInvoice().GetStatus() != "paid" {
		t.Fatalf("paid page = %v", paid)
	}
	if n := chargesOf(worker, inv.GetId()); n != 1 {
		t.Fatalf("charges = %d, want 1", n)
	}
	for _, cmd := range worker.Commands() {
		if ch := cmd.GetChargePayment(); ch != nil && ch.GetInvoiceId() == inv.GetId() && (ch.GetPaymentMethod() != "pm_sandbox_success" || ch.GetAmountMinor() != 4900 || ch.GetInvoiceId() != inv.GetId()) {
			t.Errorf("charge = %v", ch)
		}
	}
	// Paying again is refused; a second press of the button on a paid invoice is not a second charge.
	_, err = l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: invToken, Method: "mock"})
	keyOf(t, err, codes.FailedPrecondition, "billing.payment.invoice_not_payable")

	// A single-use link is consumed by the payment.
	got := l.link(c.GetLink().GetId())
	if got.GetStatus() != "paid" || got.GetPaidCount() != 1 || got.GetUseCount() != 1 || got.GetLastPaidTime() == nil {
		t.Fatalf("link after payment = %v", got)
	}
	_, err = l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: c.GetToken()})
	keyOf(t, err, codes.FailedPrecondition, "billing.payment_link.consumed")
	if again, _ := l.page(c.GetToken()); !again.GetAdhoc().GetConsumed() {
		t.Errorf("a consumed link says so: %v", again)
	}
	if linkInvoices() != 1 {
		t.Errorf("a consumed link made another invoice")
	}

	// The tenant is told, once each.
	types := l.events()
	if got := countType(types, "payment_link.created"); got != 1 {
		t.Errorf("payment_link.created x%d in %v", got, types)
	}
	if got := countType(types, "payment_link.paid"); got != 1 {
		t.Errorf("payment_link.paid x%d in %v", got, types)
	}
	if got := countType(types, "invoice.paid"); got != 1 {
		t.Errorf("invoice.paid x%d in %v", got, types)
	}
}

// chargesOf counts the charge commands a stub worker received for one invoice (workers of earlier tests share the queues).
func chargesOf(w interface {
	Commands() []*commandsv1.CommandEnvelope
}, invoiceID string) int {
	n := 0
	for _, c := range w.Commands() {
		if ch := c.GetChargePayment(); ch != nil && ch.GetInvoiceId() == invoiceID {
			n++
		}
	}
	return n
}

func countType(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

func testReusableAdhocLink(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	single := false
	c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 1000, Description: "Donation", CustomerExternalId: "cus_a", SingleUse: &single})
	if c.GetLink().GetSingleUse() || c.GetLink().GetCustomerExternalId() != "cus_a" {
		t.Fatalf("link = %v", c.GetLink())
	}
	first, second := l.start(c.GetToken()), l.start(c.GetToken())
	a, b := l.waitState(first, "open"), l.waitState(second, "open")
	if a.GetInvoice().GetId() == b.GetInvoice().GetId() || a.GetInvoice().GetNumber() == b.GetInvoice().GetNumber() {
		t.Fatalf("a reusable link makes an invoice per use: %v / %v", a.GetInvoice().GetNumber(), b.GetInvoice().GetNumber())
	}
	// The invoice is made out to the customer the link names, and their e-mail is on file for the receipt.
	if a.GetCustomerName() != "Customer cus_a" || a.GetInvoice().GetCustomerExternalId() != "cus_a" || a.GetInvoice().GetCustomerId() != l.customer("cus_a").Id {
		t.Errorf("customer = %v", a)
	}
	if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: first, Method: "mock"}); err != nil {
		t.Fatal(err)
	}
	l.waitState(first, "paid")
	// One payer's page says nothing of the other's; the link stays usable.
	if still, _ := l.page(second); still.GetPaymentState() != "open" {
		t.Errorf("the other invoice = %v", still.GetPaymentState())
	}
	got := l.link(c.GetLink().GetId())
	if got.GetStatus() != "active" || got.GetPaidCount() != 1 || got.GetUseCount() != 2 {
		t.Fatalf("link = %v", got)
	}
	third := l.start(c.GetToken())
	if p := l.waitState(third, "open"); p.GetInvoice().GetId() == a.GetInvoice().GetId() {
		t.Errorf("a paid invoice was handed out again")
	}
	// The customer's list shows the link under the customer.
	list, err := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{CustomerExternalId: "cus_a"})
	if err != nil || len(list.GetLinks()) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func testPaymentLinkCreation(t *testing.T) {
	l := newLinkEnv(t, envOpts{}, false)
	stranger := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	req := func(f func(*billingv1.CreatePaymentLinkRequest)) *billingv1.CreatePaymentLinkRequest {
		r := &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 500, Description: "Setup", Currency: "EUR"}
		if f != nil {
			f(r)
		}
		return r
	}
	_, err := l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.AmountMinor = 0 }))
	keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_amount")
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.Description = "  " }))
	keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_description")
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.Currency = "XYZ" }))
	keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_currency")
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.Kind = "subscription" }))
	keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_kind")
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.ExpiresInDays = 400 }))
	keyOf(t, err, codes.InvalidArgument, "billing.invoice_link.bad_expiry")
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.CustomerExternalId = "nobody" }))
	keyOf(t, err, codes.NotFound, "billing.payment_link.customer_not_found")
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.InvoiceId = uuid.NewString() }))
	keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_kind")
	_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "invoice"})
	keyOf(t, err, codes.InvalidArgument, "billing.payment_link.invoice_required")

	// An invoice link needs an invoice that can still be paid.
	customer := uuid.New()
	open := seedOne(t, l.tid, customer, "cus_pl", "open", "USD", "INV-2026-000301", 800, "live")
	paid := seedOne(t, l.tid, customer, "cus_pl", "paid", "USD", "INV-2026-000302", 800, "live")
	draft := seedOne(t, l.tid, customer, "cus_pl", "draft", "USD", "", 800, "live")
	_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{InvoiceId: paid.String()})
	keyOf(t, err, codes.FailedPrecondition, "billing.payment_link.invoice_not_payable")
	_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{InvoiceId: draft.String()})
	keyOf(t, err, codes.FailedPrecondition, "billing.invoice_link.draft_invoice")
	if _, err := l.svc.CreatePaymentLink(stranger, &billingv1.CreatePaymentLinkRequest{InvoiceId: open.String()}); status.Code(err) != codes.NotFound {
		t.Errorf("another tenant's invoice: %v", err)
	}
	inv := l.create(&billingv1.CreatePaymentLinkRequest{InvoiceId: open.String()})
	if inv.GetLink().GetKind() != "invoice" || inv.GetLink().GetSingleUse() || inv.GetLink().GetInvoiceId() != open.String() || inv.GetLink().GetCustomerExternalId() != "cus_pl" {
		t.Fatalf("invoice link = %v", inv.GetLink())
	}
	if d := time.Until(inv.GetLink().GetExpireTime().AsTime()); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Errorf("an invoice link lasts %v, want about 90 days", d)
	}

	// Idempotent per key: a repeat is the same link, without a token; another request under the key is refused.
	first := l.create(req(func(r *billingv1.CreatePaymentLinkRequest) { r.IdempotencyKey = "key-1" }))
	again := l.create(req(func(r *billingv1.CreatePaymentLinkRequest) { r.IdempotencyKey = "key-1" }))
	if first.GetReplayed() || !again.GetReplayed() || again.GetToken() != "" || again.GetLink().GetId() != first.GetLink().GetId() {
		t.Fatalf("first = %v, again = %v", first, again)
	}
	_, err = l.svc.CreatePaymentLink(l.mine, req(func(r *billingv1.CreatePaymentLinkRequest) { r.IdempotencyKey = "key-1"; r.AmountMinor = 501 }))
	keyOf(t, err, codes.AlreadyExists, "billing.payment_link.idempotency_conflict")
	if other, err := l.svc.CreatePaymentLink(stranger, req(func(r *billingv1.CreatePaymentLinkRequest) { r.IdempotencyKey = "key-1" })); err != nil || other.GetReplayed() {
		t.Errorf("another tenant's key is its own: %v, %v", other, err)
	}
	var made int
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM payment_links WHERE tenant_id = $1 AND idempotency_key = 'key-1'`, l.tid).Scan(&made); err != nil || made != 1 {
		t.Errorf("the key made %d links, %v", made, err)
	}
	// Only a hash is stored.
	var clear int
	if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM payment_links WHERE token_hash = $1`, []byte(first.GetToken())).Scan(&clear); err != nil || clear != 0 {
		t.Errorf("the token is stored as it is: %d, %v", clear, err)
	}

	// Listing: filters, newest first, paging; another tenant sees none of it.
	all, err := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{PageSize: 1})
	if err != nil || len(all.GetLinks()) != 1 || all.GetNextPageToken() == "" {
		t.Fatalf("page 1 = %v, %v", all, err)
	}
	next, err := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{PageSize: 1, PageToken: all.GetNextPageToken()})
	if err != nil || len(next.GetLinks()) != 1 || next.GetLinks()[0].GetId() == all.GetLinks()[0].GetId() {
		t.Fatalf("page 2 = %v, %v", next, err)
	}
	byInvoice, _ := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{InvoiceId: open.String()})
	if len(byInvoice.GetLinks()) != 1 {
		t.Errorf("by invoice = %v", byInvoice)
	}
	if adhoc, _ := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{Kind: "adhoc", Status: "active"}); len(adhoc.GetLinks()) != 1 {
		t.Errorf("ad hoc and active = %v", adhoc)
	}
	if none, _ := l.svc.ListPaymentLinks(stranger, &billingv1.ListPaymentLinksRequest{}); len(none.GetLinks()) != 1 { // its own key-1 link only
		t.Errorf("another tenant lists %v", none)
	}
	if _, err := l.svc.GetPaymentLink(stranger, &billingv1.GetPaymentLinkRequest{LinkId: first.GetLink().GetId()}); status.Code(err) != codes.NotFound {
		t.Errorf("another tenant reads the link: %v", err)
	}
	if _, err := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{Status: "bogus"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad status: %v", err)
	}
}

func testPaymentLinkRevokeAndExpiry(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	stranger := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	sagaStatus := func(invoiceID string) (string, string) {
		var st, ss string
		if err := shared.sql.QueryRow(context.Background(), `SELECT i.status, COALESCE(s.state, '') FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id WHERE i.tenant_id = $2 AND i.id = $1`, invoiceID, l.tid).Scan(&st, &ss); err != nil {
			t.Fatal(err)
		}
		return st, ss
	}

	// Revoking an ad-hoc link: the link and the invoice links it handed out stop, and the unpaid invoice is voided.
	c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 700, Description: "Revoked"})
	child := l.start(c.GetToken())
	invoiceID := l.waitState(child, "open").GetInvoice().GetId()
	if _, err := l.svc.RevokePaymentLink(stranger, &billingv1.RevokePaymentLinkRequest{LinkId: c.GetLink().GetId()}); status.Code(err) != codes.NotFound {
		t.Errorf("another tenant revokes: %v", err)
	}
	r, err := l.svc.RevokePaymentLink(l.mine, &billingv1.RevokePaymentLinkRequest{LinkId: c.GetLink().GetId()})
	if err != nil || r.GetLink().GetStatus() != "revoked" || r.GetLink().GetRevokeTime() == nil {
		t.Fatalf("revoke = %v, %v", r, err)
	}
	if _, err := l.svc.RevokePaymentLink(l.mine, &billingv1.RevokePaymentLinkRequest{LinkId: c.GetLink().GetId()}); err != nil {
		t.Errorf("revoking twice: %v", err)
	}
	for name, tok := range map[string]string{"link": c.GetToken(), "child": child} {
		if _, err := l.page(tok); status.Code(err) != codes.NotFound {
			t.Errorf("revoked %s: %v", name, err)
		}
	}
	if _, err := l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: c.GetToken()}); status.Code(err) != codes.NotFound {
		t.Errorf("start on a revoked link: %v", err)
	}
	testkit.Eventually(t, 15*time.Second, "the unpaid invoice to be voided", func() bool { st, ss := sagaStatus(invoiceID); return st == "void" && ss == "VOIDED" })

	// Expiry: an expired link says nothing; the sweep tells the tenant once and voids what is unpaid.
	e := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 800, Description: "Expires"})
	expChild := l.start(e.GetToken())
	expInvoice := l.waitState(expChild, "open").GetInvoice().GetId()
	paidLink := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 900, Description: "Paid then expired"})
	paidChild := l.start(paidLink.GetToken())
	l.waitState(paidChild, "open")
	if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: paidChild, Method: "mock"}); err != nil {
		t.Fatal(err)
	}
	l.waitState(paidChild, "paid")
	if _, err := shared.sql.Exec(context.Background(), `UPDATE payment_links SET expires_at = now() - interval '1 second' WHERE tenant_id = $1 AND (id = ANY($2) OR parent_id = ANY($2))`,
		l.tid, []uuid.UUID{uuid.MustParse(e.GetLink().GetId()), uuid.MustParse(paidLink.GetLink().GetId())}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.page(e.GetToken()); status.Code(err) != codes.NotFound {
		t.Errorf("expired: %v", err)
	}
	for i := 0; i < 2; i++ { // the second pass finds nothing left to do
		if err := l.hosted.ExpireLinks(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	types := l.events()
	if n := countType(types, "payment_link.expired"); n != 1 {
		t.Errorf("payment_link.expired x%d in %v, want one (the unpaid ad-hoc link; the paid single-use one was used, not expired)", n, types)
	}
	testkit.Eventually(t, 15*time.Second, "the expired link's invoice to be voided", func() bool { st, _ := sagaStatus(expInvoice); return st == "void" })
	if got := l.link(e.GetLink().GetId()); got.GetStatus() != "expired" {
		t.Errorf("status = %s", got.GetStatus())
	}
	if got := l.link(paidLink.GetLink().GetId()); got.GetStatus() != "paid" {
		t.Errorf("the paid link = %s", got.GetStatus())
	}
}

func testPortalIsolation(t *testing.T) {
	one := newLinkEnv(t, envOpts{}, false)
	two := newLinkEnv(t, envOpts{}, false)
	a, b := uuid.MustParse(one.customer("cus_a").Id), uuid.MustParse(one.customer("cus_b").Id)
	mineA := seedOne(t, one.tid, a, "cus_a", "open", "USD", "INV-2026-000401", 1100, "live")
	paidA := seedOne(t, one.tid, a, "cus_a", "paid", "USD", "INV-2026-000402", 1200, "live")
	_ = seedOne(t, one.tid, a, "cus_a", "draft", "USD", "", 1300, "live")
	theirsB := seedOne(t, one.tid, b, "cus_b", "open", "USD", "INV-2026-000403", 1400, "live")
	otherTenant := seedOne(t, two.tid, a, "cus_a", "open", "USD", "INV-2026-000404", 1500, "live") // the same customer id in another tenant

	p, err := one.svc.CreatePortalLink(one.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_a"})
	if err != nil || len(p.GetToken()) < 40 || p.GetCustomerId() != a.String() {
		t.Fatalf("portal = %v, %v", p, err)
	}
	if d := time.Until(p.GetExpireTime().AsTime()); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Errorf("a portal link lasts %v, want about 30 days", d)
	}
	_, err = one.svc.CreatePortalLink(one.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "nobody"})
	keyOf(t, err, codes.NotFound, "billing.portal_link.customer_not_found")
	_, err = one.svc.CreatePortalLink(one.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_a", ExpiresInDays: 999})
	keyOf(t, err, codes.InvalidArgument, "billing.invoice_link.bad_expiry")

	page, err := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{PortalToken: p.GetToken()})
	if err != nil || page.GetTenantId() != one.tid.String() || page.GetCustomerId() != a.String() || page.GetCustomerName() != "" && page.GetCustomerName() != "Customer cus_a" {
		t.Fatalf("portal page = %v, %v", page, err)
	}
	ids := map[string]string{}
	for _, i := range page.GetInvoices() {
		ids[i.GetId()] = i.GetStatus()
	}
	if len(ids) != 2 || ids[mineA.String()] != "open" || ids[paidA.String()] != "paid" {
		t.Fatalf("the portal lists %v, want the customer's two finalized invoices and nothing else", ids)
	}

	// One customer's invoice, through the portal.
	if inv, err := one.svc.GetHostedInvoice(one.visitor, &billingv1.GetHostedInvoiceRequest{PortalToken: p.GetToken(), InvoiceId: mineA.String()}); err != nil || inv.GetInvoice().GetNumber() != "INV-2026-000401" {
		t.Fatalf("own invoice = %v, %v", inv, err)
	}
	// Not another customer's, not another tenant's, not a draft, not made up.
	for name, id := range map[string]string{"another customer": theirsB.String(), "another tenant": otherTenant.String(), "unknown": uuid.NewString(), "malformed": "x"} {
		if _, err := one.svc.GetHostedInvoice(one.visitor, &billingv1.GetHostedInvoiceRequest{PortalToken: p.GetToken(), InvoiceId: id}); status.Code(err) != codes.NotFound {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := one.svc.GetHostedInvoicePdfUrl(one.visitor, &billingv1.GetHostedInvoicePdfUrlRequest{PortalToken: p.GetToken(), InvoiceId: id}); status.Code(err) != codes.NotFound {
			t.Errorf("pdf of %s: %v", name, err)
		}
		if _, err := one.svc.RequestHostedPayment(one.visitor, &billingv1.RequestHostedPaymentRequest{PortalToken: p.GetToken(), InvoiceId: id, Method: "mock"}); status.Code(err) != codes.NotFound {
			t.Errorf("paying %s: %v", name, err)
		}
	}
	// A token is a token of one kind.
	link := one.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "x"})
	if _, err := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{PortalToken: link.GetToken()}); status.Code(err) != codes.NotFound {
		t.Errorf("a payment link opens the portal: %v", err)
	}
	if _, err := one.page(p.GetToken()); status.Code(err) != codes.NotFound {
		t.Errorf("a portal token opens a payment link: %v", err)
	}
	if _, err := one.svc.GetHostedInvoice(one.visitor, &billingv1.GetHostedInvoiceRequest{Token: link.GetToken(), PortalToken: p.GetToken(), InvoiceId: mineA.String()}); status.Code(err) != codes.NotFound {
		t.Errorf("two credentials at once: %v", err)
	}
	// The other customer's portal is theirs.
	pb, err := one.svc.CreatePortalLink(one.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_b", ExpiresInDays: 5})
	if err != nil {
		t.Fatal(err)
	}
	if page, _ := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{PortalToken: pb.GetToken()}); len(page.GetInvoices()) != 1 || page.GetInvoices()[0].GetId() != theirsB.String() {
		t.Errorf("customer b's portal = %v", page)
	}
	// The tenant cannot revoke another tenant's customers' links.
	if _, err := two.svc.RevokePortalLinks(two.mine, &billingv1.RevokePortalLinksRequest{CustomerExternalId: "cus_a"}); err != nil {
		t.Fatal(err) // its own customer of the same name has no links: nothing revoked, and our link stays
	}
	if _, err := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{PortalToken: p.GetToken()}); err != nil {
		t.Errorf("the portal link after another tenant revoked its own: %v", err)
	}

	// Expiry and revocation.
	if _, err := shared.sql.Exec(context.Background(), `UPDATE portal_links SET expires_at = now() - interval '1 second' WHERE tenant_id = $1 AND customer_id = $2`, one.tid, b); err != nil {
		t.Fatal(err)
	}
	if _, err := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{PortalToken: pb.GetToken()}); status.Code(err) != codes.NotFound {
		t.Errorf("expired portal: %v", err)
	}
	r, err := one.svc.RevokePortalLinks(one.mine, &billingv1.RevokePortalLinksRequest{CustomerExternalId: "cus_a"})
	if err != nil || r.GetRevoked() != 1 {
		t.Fatalf("revoke = %v, %v", r, err)
	}
	if _, err := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{PortalToken: p.GetToken()}); status.Code(err) != codes.NotFound {
		t.Errorf("revoked portal: %v", err)
	}
	if _, err := one.svc.GetPortal(one.visitor, &billingv1.GetPortalRequest{}); status.Code(err) != codes.NotFound {
		t.Errorf("no token: %v", err)
	}
}

func testSettleInvoicePayment(t *testing.T) {
	l := newLinkEnv(t, envOpts{}, false) // a live tenant
	first := l.startWorkers(nil)
	stranger := tenant.NewContext(context.Background(), tenant.Info{ID: uuid.New()})
	c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 2500, Description: "Live payment", Currency: "EUR"})
	child := l.start(c.GetToken())
	page := l.waitState(child, "open")
	invoiceID := page.GetInvoice().GetId()
	if !strings.HasPrefix(page.GetInvoice().GetNumber(), "INV-") || page.GetInvoice().GetCurrency() != "EUR" || page.GetEnvironment() != "live" {
		t.Fatalf("page = %v", page)
	}

	// A live tenant has no mock payment; other methods are for payments-worker.
	_, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "mock"})
	keyOf(t, err, codes.FailedPrecondition, "billing.payment.method_unavailable")
	r, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "checkout"})
	if err != nil || r.GetAction() != "provider" || r.GetPayment().GetAmountMinor() != 2500 || r.GetPayment().GetCurrency() != "EUR" || r.GetPayment().GetSagaId() == "" ||
		r.GetPayment().GetTenantId() != l.tid.String() || r.GetPayment().GetEnvironment() != "live" || r.GetPayment().GetDescription() != "Live payment" {
		t.Fatalf("checkout context = %v, %v", r, err)
	}
	if got := l.waitState(child, "open"); got.GetInvoice().GetSagaState() != "AWAITING_PAYMENT" {
		t.Errorf("asking for a checkout moved the saga: %v", got.GetInvoice().GetSagaState())
	}

	// What the provider confirms must be this invoice's amount.
	settle := func(ctx context.Context, amount int64, currency string) (*billingv1.SettleInvoicePaymentResponse, error) {
		return l.svc.SettleInvoicePayment(ctx, &billingv1.SettleInvoicePaymentRequest{InvoiceId: invoiceID, Provider: "paddle", ProviderRef: "txn_1", AmountMinor: amount, Currency: currency})
	}
	_, err = settle(l.mine, 2499, "EUR")
	keyOf(t, err, codes.FailedPrecondition, "billing.payment.amount_mismatch")
	_, err = settle(l.mine, 2500, "USD")
	keyOf(t, err, codes.FailedPrecondition, "billing.payment.amount_mismatch")
	if _, err := settle(stranger, 2500, "EUR"); status.Code(err) != codes.NotFound {
		t.Errorf("another tenant settles: %v", err)
	}
	if got, _ := l.page(child); got.GetPaymentState() != "open" {
		t.Fatalf("a refused settle changed the state: %v", got.GetPaymentState())
	}
	ok, err := settle(l.mine, 2500, "EUR")
	if err != nil || !ok.GetSettled() {
		t.Fatalf("settle = %v, %v", ok, err)
	}
	paid := l.waitState(child, "paid")
	if paid.GetInvoice().GetStatus() != "paid" {
		t.Fatalf("paid page = %v", paid)
	}
	// A second confirmation is not a second payment; the operator sees it as a discrepancy in payments-worker.
	again, err := settle(l.mine, 2500, "EUR")
	if err != nil || again.GetSettled() {
		t.Errorf("settling a paid invoice = %v, %v", again, err)
	}
	if n := countType(l.events(), "payment_link.paid"); n != 1 {
		t.Errorf("payment_link.paid x%d", n)
	}
	if got := l.link(c.GetLink().GetId()); got.GetStatus() != "paid" || got.GetPaidCount() != 1 {
		t.Errorf("link = %v", got)
	}

	// A demo stack lets a live tenant use the mock.
	demo := newLinkEnv(t, envOpts{}, true)
	w := demo.startWorkers(nil)
	dc := demo.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 300, Description: "Demo"})
	dChild := demo.start(dc.GetToken())
	dInvoice := demo.waitState(dChild, "open").GetInvoice().GetId()
	if _, err := demo.svc.RequestHostedPayment(demo.visitor, &billingv1.RequestHostedPaymentRequest{Token: dChild, Method: "mock"}); err != nil {
		t.Fatal(err)
	}
	demo.waitState(dChild, "paid")
	// Both stub workers consume the same queues: together they saw the one charge.
	if n := chargesOf(w, dInvoice) + chargesOf(first, dInvoice); n != 1 {
		t.Errorf("demo charges = %d", n)
	}
}
