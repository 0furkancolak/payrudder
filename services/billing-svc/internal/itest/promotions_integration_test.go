//go:build integration

package itest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/credits/query"
)

// fakeCode is a promotion code of the fake plans-svc: the same rules as plans-svc's, enough for billing to be tested against.
type fakeCode struct {
	percentBps, amount, bonusBps int64
	currency                     string
	max, used                    int
	expired, firstOnly, onceEach bool
	restore                      bool
	minAmount                    int64
	packs                        []string
	uses                         map[string]int
}

type fakeRedemption struct {
	code     *fakeCode
	customer string
	reversed bool
}

// promoState is the promotions of the fake plans-svc of one world.
type promoState struct {
	mu         sync.Mutex
	codes      map[string]*fakeCode // by upper-case text
	grants     map[string]string    // customer external id -> code
	redeemed   map[string]*fakeRedemption
	requests   []*plansv1.RedeemPromotionRequest
	reversals  []string
	claimCalls int
}

func (p *promoState) add(text string, c *fakeCode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.codes == nil {
		p.codes, p.grants, p.redeemed = map[string]*fakeCode{}, map[string]string{}, map[string]*fakeRedemption{}
	}
	c.uses = map[string]int{}
	p.codes[strings.ToUpper(text)] = c
}

func (p *promoState) used(text string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.codes[strings.ToUpper(text)].used
}

func refuse(code codes.Code, key string) error { return errkey.New(code, key, key) }

// RedeemPromotion is plans-svc's RedeemPromotion, as far as billing depends on it.
func (f fakeAll) RedeemPromotion(_ context.Context, in *plansv1.RedeemPromotionRequest, _ ...grpc.CallOption) (*plansv1.RedeemPromotionResponse, error) {
	p := &f.w.promo
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, in)
	if !in.GetPreview() {
		p.claimCalls++
		if r, ok := p.redeemed[in.GetTargetId()]; ok && !r.reversed {
			return &plansv1.RedeemPromotionResponse{Applied: true}, nil
		}
	}
	text := strings.ToUpper(in.GetCode())
	fromGrant := false
	if text == "" {
		if text = p.grants[in.GetCustomerExternalId()]; text == "" {
			return &plansv1.RedeemPromotionResponse{}, nil
		}
		fromGrant = true
	}
	c, ok := p.codes[text]
	switch {
	case !ok:
		return nil, refuse(codes.NotFound, "coupon.not_found")
	case c.expired:
		return nil, refuse(codes.FailedPrecondition, "coupon.expired")
	case c.max > 0 && c.used >= c.max:
		return nil, refuse(codes.FailedPrecondition, "coupon.exhausted")
	case c.firstOnly && in.GetPriorPurchases():
		return nil, refuse(codes.FailedPrecondition, "coupon.first_purchase_only")
	case c.onceEach && c.uses[in.GetCustomerExternalId()] > 0:
		return nil, refuse(codes.FailedPrecondition, "coupon.already_used")
	case c.amount > 0 && c.currency != in.GetCurrency():
		return nil, refuse(codes.FailedPrecondition, "coupon.currency_mismatch")
	case c.minAmount > 0 && in.GetAmountMinor() < c.minAmount:
		return nil, refuse(codes.FailedPrecondition, "coupon.below_minimum")
	case len(c.packs) > 0 && !contains(c.packs, in.GetItem()):
		return nil, refuse(codes.FailedPrecondition, "coupon.not_applicable")
	}
	var discount, bonus int64
	switch {
	case c.percentBps > 0:
		discount = min(subscription.ScaleHalfEven(in.GetAmountMinor(), c.percentBps, 10000), in.GetAmountMinor())
	case c.amount > 0:
		discount = min(c.amount, in.GetAmountMinor())
	case c.bonusBps > 0:
		bonus = in.GetCredits() * c.bonusBps / 10000
	}
	if !in.GetPreview() {
		c.used++
		c.uses[in.GetCustomerExternalId()]++
		p.redeemed[in.GetTargetId()] = &fakeRedemption{code: c, customer: in.GetCustomerExternalId()}
		if fromGrant {
			delete(p.grants, in.GetCustomerExternalId())
		}
	}
	return &plansv1.RedeemPromotionResponse{Applied: true, CouponCode: "C-" + text, PromotionCode: map[bool]string{true: "", false: text}[fromGrant], DiscountMinor: discount,
		BonusCredits: bonus, TotalMinor: in.GetAmountMinor() - discount, Currency: in.GetCurrency()}, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (f fakeAll) ReversePromotion(_ context.Context, in *plansv1.ReversePromotionRequest, _ ...grpc.CallOption) (*plansv1.ReversePromotionResponse, error) {
	p := &f.w.promo
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.redeemed[in.GetTargetId()]
	if !ok || r.reversed {
		return &plansv1.ReversePromotionResponse{}, nil
	}
	r.reversed = true
	if r.code.restore {
		r.code.used--
		r.code.uses[r.customer]--
	}
	p.reversals = append(p.reversals, in.GetTargetId())
	return &plansv1.ReversePromotionResponse{Reversed: true}, nil
}

func (l *linkEnv) quote(token, code string) (*billingv1.QuoteLinkPromotionResponse, error) {
	return l.svc.QuoteLinkPromotion(l.visitor, &billingv1.QuoteLinkPromotionRequest{Token: token, PromotionCode: code})
}

func (l *linkEnv) startWith(token, code string) (string, error) {
	r, err := l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: token, PromotionCode: code})
	return r.GetInvoiceToken(), err
}

type invoiceRow struct {
	subtotal, total, bonus int64
	coupon, code, target   string
	lines                  []string
	amounts                []int64
}

func (l *linkEnv) invoiceOf(token string) invoiceRow {
	l.t.Helper()
	page, err := l.page(token)
	if err != nil {
		l.t.Fatal(err)
	}
	var r invoiceRow
	id := page.GetInvoice().GetId()
	var coupon, code, target *string
	if err := shared.system.QueryRow(context.Background(), `SELECT subtotal_minor, total_minor, bonus_credits, coupon_code, promotion_code, promotion_target FROM invoices WHERE tenant_id = $1 AND id = $2`,
		l.tid, id).Scan(&r.subtotal, &r.total, &r.bonus, &coupon, &code, &target); err != nil {
		l.t.Fatal(err)
	}
	for _, s := range []struct {
		dst *string
		src *string
	}{{&r.coupon, coupon}, {&r.code, code}, {&r.target, target}} {
		if s.src != nil {
			*s.dst = *s.src
		}
	}
	for _, ln := range page.GetInvoice().GetLines() {
		r.lines, r.amounts = append(r.lines, ln.GetKind()+": "+ln.GetDescription()), append(r.amounts, ln.GetAmountMinor())
	}
	return r
}

func testPromotionCodesOnPaymentLinks(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	p := &l.w.promo
	p.add("SAVE10", &fakeCode{percentBps: 1000})
	p.add("MINUS20", &fakeCode{amount: 2000, currency: "USD"})
	p.add("EURONLY", &fakeCode{amount: 500, currency: "EUR"})
	p.add("OLD", &fakeCode{percentBps: 1000, expired: true})
	p.add("BIG", &fakeCode{percentBps: 1000, minAmount: 50000})
	p.add("FREE", &fakeCode{percentBps: 10000})
	p.add("TWICE", &fakeCode{percentBps: 500, max: 1})
	p.add("RESTORES", &fakeCode{percentBps: 500, max: 1, restore: true})
	p.add("KEEPS", &fakeCode{percentBps: 500, max: 1})

	t.Run("the page quotes a code, and the invoice has a DISCOUNT line and the discounted total", func(t *testing.T) {
		c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 10000, Description: "Workshop", CustomerExternalId: "cus_a"})
		q, err := l.quote(c.GetToken(), "save10")
		if err != nil || !q.GetApplied() || q.GetDiscountMinor() != 1000 || q.GetTotalMinor() != 9000 {
			t.Fatalf("quote = %v, %v", q, err)
		}
		if p.used("SAVE10") != 0 {
			t.Fatalf("a quote used %d redemptions", p.used("SAVE10"))
		}
		child, err := l.startWith(c.GetToken(), "save10")
		if err != nil {
			t.Fatal(err)
		}
		inv := l.invoiceOf(child)
		if inv.total != 9000 || inv.subtotal != 9000 || inv.coupon != "C-SAVE10" || inv.code != "SAVE10" || inv.target != "payment" || len(inv.amounts) != 2 || inv.amounts[0] != 10000 ||
			inv.amounts[1] != -1000 || !strings.HasPrefix(inv.lines[1], "DISCOUNT: Promotion code SAVE10") {
			t.Fatalf("invoice = %+v", inv)
		}
		if p.used("SAVE10") != 1 {
			t.Errorf("redemptions = %d", p.used("SAVE10"))
		}
		l.waitState(child, "open")
		if r, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "mock"}); err != nil || r.GetAction() != "started" {
			t.Fatalf("pay = %v, %v", r, err)
		}
		if paid := l.waitState(child, "paid"); paid.GetInvoice().GetTotalMinor() != 9000 {
			t.Errorf("paid invoice = %v", paid.GetInvoice())
		}
	})

	t.Run("a refused code makes no invoice and uses no redemption", func(t *testing.T) {
		c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 10000, Description: "Workshop", CustomerExternalId: "cus_a", SingleUse: boolPtr(false)})
		for code, want := range map[string]string{"EURONLY": "coupon.currency_mismatch", "OLD": "coupon.expired", "NOPE": "coupon.not_found", "BIG": "coupon.below_minimum"} {
			_, err := l.startWith(c.GetToken(), code)
			if errkey.Of(err) != want {
				t.Errorf("%s: %v, want %s", code, err, want)
			}
			if _, err := l.quote(c.GetToken(), code); errkey.Of(err) != want {
				t.Errorf("quote of %s: %v, want %s", code, err, want)
			}
		}
		if got := l.link(c.GetLink().GetId()).GetUseCount(); got != 0 {
			t.Errorf("the link made %d invoices from refused codes", got)
		}
		// A code that takes the whole price leaves nothing to pay: refused before a redemption is used.
		_, err := l.startWith(c.GetToken(), "FREE")
		keyOf(t, err, codes.FailedPrecondition, "billing.payment_link.free_total")
		if p.used("FREE") != 0 {
			t.Errorf("FREE used %d redemptions", p.used("FREE"))
		}
		// Without a code the link works as before.
		if child, err := l.startWith(c.GetToken(), ""); err != nil || l.invoiceOf(child).total != 10000 {
			t.Errorf("no code: %v", err)
		}
	})

	t.Run("an amount code takes its amount and a code of another currency is refused", func(t *testing.T) {
		c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 10000, Description: "Workshop", CustomerExternalId: "cus_b"})
		child, err := l.startWith(c.GetToken(), "MINUS20")
		if err != nil {
			t.Fatal(err)
		}
		if inv := l.invoiceOf(child); inv.total != 8000 || inv.amounts[1] != -2000 {
			t.Errorf("invoice = %+v", inv)
		}
	})

	t.Run("a link made with a code applies it, and the payer's own code wins", func(t *testing.T) {
		made := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 20000, Description: "Setup", CustomerExternalId: "cus_a", PromotionCode: "SAVE10", SingleUse: boolPtr(false)})
		if made.GetLink().GetPromotionCode() != "SAVE10" {
			t.Fatalf("link = %v", made.GetLink())
		}
		landing, err := l.page(made.GetToken())
		if err != nil || landing.GetAdhoc().GetPromotionCode() != "SAVE10" {
			t.Fatalf("page = %v, %v", landing, err)
		}
		if q, err := l.quote(made.GetToken(), ""); err != nil || q.GetDiscountMinor() != 2000 {
			t.Errorf("quote of the link's code = %v, %v", q, err)
		}
		first, err := l.startWith(made.GetToken(), "")
		if err != nil || l.invoiceOf(first).total != 18000 {
			t.Fatalf("link code: %v", err)
		}
		// The link is reusable and the first invoice is unpaid: continuing again makes a second invoice, with the code the payer typed.
		second, err := l.startWith(made.GetToken(), "TWICE")
		if err != nil {
			t.Fatal(err)
		}
		if inv := l.invoiceOf(second); inv.total != 19000 || inv.coupon != "C-TWICE" {
			t.Errorf("second invoice = %+v", inv)
		}
		// A code that does not work for the link's own amount is refused when the link is made; an invoice link takes none.
		_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "x", PromotionCode: "EURONLY"})
		keyOf(t, err, codes.FailedPrecondition, "coupon.currency_mismatch")
		_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "x", PromotionCode: "no spaces"})
		keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_promotion_code")
		_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "invoice", InvoiceId: uuid.NewString(), PromotionCode: "SAVE10"})
		keyOf(t, err, codes.InvalidArgument, "billing.payment_link.bad_promotion_code")
	})

	t.Run("a void queues the reversal, and the code's setting decides whether the place comes back", func(t *testing.T) {
		for _, code := range []string{"RESTORES", "KEEPS"} {
			c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 10000, Description: "Workshop", CustomerExternalId: "cus_a"})
			child, err := l.startWith(c.GetToken(), code)
			if err != nil {
				t.Fatal(err)
			}
			l.waitState(child, "open")
			if p.used(code) != 1 {
				t.Fatalf("%s used %d", code, p.used(code))
			}
			// Nobody else can have the last place while the purchase stands.
			other := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 10000, Description: "Workshop", CustomerExternalId: "cus_b"})
			_, err = l.startWith(other.GetToken(), code)
			keyOf(t, err, codes.FailedPrecondition, "coupon.exhausted")
			// Revoking the link voids the unpaid invoice.
			if _, err := l.svc.RevokePaymentLink(l.mine, &billingv1.RevokePaymentLinkRequest{LinkId: c.GetLink().GetId()}); err != nil {
				t.Fatal(err)
			}
			eventually(t, 30*time.Second, "the reversal of "+code+" to be queued", func() bool {
				var n int
				if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM promotion_reversals WHERE tenant_id = $1 AND done_at IS NULL AND target_kind = 'payment'`, l.tid).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n > 0
			})
			if err := l.mgr.ReversePromotions(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err = l.startWith(other.GetToken(), code)
			if code == "RESTORES" && err != nil {
				t.Errorf("RESTORES after the void: %v", err)
			}
			if code == "KEEPS" {
				keyOf(t, err, codes.FailedPrecondition, "coupon.exhausted")
			}
		}
		var pending int
		if err := shared.system.QueryRow(context.Background(), `SELECT count(*) FROM promotion_reversals WHERE tenant_id = $1 AND done_at IS NULL`, l.tid).Scan(&pending); err != nil || pending != 0 {
			t.Errorf("reversals still pending: %d, %v", pending, err)
		}
		// A second pass has nothing to do.
		before := len(p.reversals)
		if err := l.mgr.ReversePromotions(context.Background()); err != nil || len(p.reversals) != before {
			t.Errorf("second pass: %v", err)
		}
	})
}

func boolPtr(b bool) *bool { return &b }

func testPromotionCodesOnCreditPurchases(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	p := &l.w.promo
	p.add("PCT20", &fakeCode{percentBps: 2000})
	p.add("BONUS25", &fakeCode{bonusBps: 2500})
	p.add("NEWBIE", &fakeCode{percentBps: 1000, firstOnly: true})
	p.add("ONEPACK", &fakeCode{percentBps: 1000})
	a, b := uuid.MustParse(l.customer("cus_a").Id), uuid.MustParse(l.customer("cus_b").Id)
	starter := l.pack("Starter", 1000, 500, "EUR", 30)
	other := l.pack("Other", 10, 50, "EUR", 0)
	p.add("ONLYSTARTER", &fakeCode{percentBps: 1000, packs: []string{starter.GetId()}})
	buy := func(cust string, pack *billingv1.CreditPack, qty int32, code string) (string, error) {
		c := l.create(&billingv1.CreatePaymentLinkRequest{PackId: pack.GetId(), CustomerExternalId: cust, Quantity: qty})
		return l.startWith(c.GetToken(), code)
	}
	pay := func(child string) {
		t.Helper()
		l.waitState(child, "open")
		if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "mock"}); err != nil {
			t.Fatal(err)
		}
		l.waitState(child, "paid")
	}

	t.Run("a discount lowers the price and the credits are the pack's", func(t *testing.T) {
		child, err := buy("cus_a", starter, 2, "pct20")
		if err != nil {
			t.Fatal(err)
		}
		inv := l.invoiceOf(child)
		if inv.total != 800 || inv.amounts[1] != -200 || inv.target != "credit_purchase" {
			t.Fatalf("invoice = %+v", inv)
		}
		pay(child)
		if got := balanceOf(t, l.tid, a); got != 2000 {
			t.Errorf("credits = %d, want the pack's 2000", got)
		}
	})

	t.Run("bonus credits go into the same lot and expire with it", func(t *testing.T) {
		child, err := buy("cus_b", starter, 1, "bonus25")
		if err != nil {
			t.Fatal(err)
		}
		inv := l.invoiceOf(child)
		if inv.total != 500 || inv.bonus != 250 || len(inv.amounts) != 1 {
			t.Fatalf("invoice = %+v; a bonus changes no price", inv)
		}
		pay(child)
		bal, err := query.BalanceOf(context.Background(), shared.app, l.tid, b)
		if err != nil || bal.Credits != 1250 || len(bal.Lots) != 1 {
			t.Fatalf("balance = %+v, %v", bal, err)
		}
		lot := bal.Lots[0]
		if lot.Granted != 1250 || lot.ExpiresAt == nil || lot.ExpiresAt.Sub(l.clk.Now().AddDate(0, 0, 30)).Abs() > time.Hour {
			t.Errorf("lot = %+v, want 1250 credits expiring in 30 days", lot)
		}
		purchased := l.eventData("credits.purchased")
		last := purchased[len(purchased)-1]
		if last["credits"] != float64(1250) || last["bonus_credits"] != float64(250) {
			t.Errorf("credits.purchased = %v", last)
		}
	})

	t.Run("restrictions: first purchase only is judged from the customer's paid invoices, and a pack restriction from the pack", func(t *testing.T) {
		// cus_a has paid and cus_b has paid (above): neither is a first purchase any more.
		_, err := buy("cus_a", starter, 1, "newbie")
		keyOf(t, err, codes.FailedPrecondition, "coupon.first_purchase_only")
		_, err = buy("cus_a", other, 1, "onlystarter")
		keyOf(t, err, codes.FailedPrecondition, "coupon.not_applicable")
		if child, err := buy("cus_a", starter, 1, "onlystarter"); err != nil || l.invoiceOf(child).total != 450 {
			t.Errorf("the right pack: %v", err)
		}
	})

	t.Run("the portal buys with a code, and a coupon waiting for the customer is used without one", func(t *testing.T) {
		pb, err := l.svc.CreatePortalLink(l.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_b"})
		if err != nil {
			t.Fatal(err)
		}
		r, err := l.svc.StartPortalPurchase(l.visitor, &billingv1.StartPortalPurchaseRequest{PortalToken: pb.GetToken(), PackId: other.GetId(), Quantity: 1, PromotionCode: "pct20"})
		if err != nil {
			t.Fatal(err)
		}
		if inv := l.invoiceOf(r.GetInvoiceToken()); inv.total != 40 || inv.code != "PCT20" {
			t.Errorf("portal invoice = %+v", inv)
		}
		// A referral programme (phase 47) names a coupon for a customer; the next checkout without a code uses it.
		p.mu.Lock()
		p.grants["cus_a"] = "BONUS25"
		p.mu.Unlock()
		child, err := buy("cus_a", other, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if inv := l.invoiceOf(child); inv.bonus != 2 || inv.coupon != "C-BONUS25" || inv.code != "" {
			t.Errorf("invoice with the waiting coupon = %+v", inv)
		}
		// Used once: the next purchase has none.
		if child, err := buy("cus_a", other, 1, ""); err != nil || l.invoiceOf(child).coupon != "" {
			t.Errorf("the coupon was used again: %v", err)
		}
	})
}
