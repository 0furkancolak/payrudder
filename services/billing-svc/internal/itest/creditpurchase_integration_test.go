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
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/query"
)

func (l *linkEnv) pack(name string, credits, price int64, currency string, validity int32) *billingv1.CreditPack {
	l.t.Helper()
	r, err := l.svc.CreateCreditPack(l.mine, &billingv1.CreateCreditPackRequest{Name: name, Credits: credits, PriceMinor: price, Currency: currency, ValidityDays: validity})
	if err != nil {
		l.t.Fatal(err)
	}
	return r.GetPack()
}

// eventData returns the data of every recorded event of a type for this tenant.
func (l *linkEnv) eventData(typ string) []map[string]any {
	l.t.Helper()
	l.events()
	var out []map[string]any
	for _, r := range allEvents.recorded(l.tid) {
		if r.GetType() != typ {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(r.GetDataJson()), &m); err != nil {
			l.t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func testCreditPackIsBoughtThroughAPaymentLink(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	cid := uuid.MustParse(l.customer("cus_a").Id)
	starter := l.pack("Starter", 1000, 500, "EUR", 30)
	forever := l.pack("Forever", 50, 100, "EUR", 0)
	withdrawn := l.pack("Old", 10, 100, "EUR", 0)
	if err := command.SetPackActive(context.Background(), shared.app, l.tid, uuid.MustParse(withdrawn.GetId()), false); err != nil {
		t.Fatal(err)
	}

	// What a client may not do: name a price, skip the customer, sell a withdrawn or foreign pack, or buy a silly quantity.
	for name, tc := range map[string]struct {
		req  *billingv1.CreatePaymentLinkRequest
		code codes.Code
		key  string
	}{
		"a price":           {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: starter.GetId(), CustomerExternalId: "cus_a", AmountMinor: 1}, codes.InvalidArgument, "billing.payment_link.bad_kind"},
		"a currency":        {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: starter.GetId(), CustomerExternalId: "cus_a", Currency: "USD"}, codes.InvalidArgument, "billing.payment_link.bad_kind"},
		"no pack":           {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", CustomerExternalId: "cus_a"}, codes.InvalidArgument, "billing.payment_link.pack_required"},
		"no customer":       {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: starter.GetId()}, codes.InvalidArgument, "billing.payment_link.customer_required"},
		"unknown customer":  {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: starter.GetId(), CustomerExternalId: "nobody"}, codes.NotFound, "billing.payment_link.customer_not_found"},
		"unknown pack":      {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: uuid.NewString(), CustomerExternalId: "cus_a"}, codes.NotFound, "billing.credits.pack_not_found"},
		"withdrawn pack":    {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: withdrawn.GetId(), CustomerExternalId: "cus_a"}, codes.FailedPrecondition, "billing.credits.pack_inactive"},
		"quantity zero-ish": {&billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: starter.GetId(), CustomerExternalId: "cus_a", Quantity: 1001}, codes.InvalidArgument, "billing.credits.bad_quantity"},
		"pack on adhoc":     {&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", PackId: starter.GetId(), AmountMinor: 100, Description: "x"}, codes.InvalidArgument, "billing.payment_link.bad_kind"},
	} {
		_, err := l.svc.CreatePaymentLink(l.mine, tc.req)
		if status.Code(err) != tc.code {
			t.Errorf("%s: %v", name, err)
			continue
		}
		keyOf(t, err, tc.code, tc.key)
	}
	// A pack of another tenant is not found: tenants do not sell each other's packs.
	other := newLinkEnv(t, envOpts{sandbox: true}, false)
	foreign := other.pack("Foreign", 1, 1, "EUR", 0)
	_, err := l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: foreign.GetId(), CustomerExternalId: "cus_a"})
	keyOf(t, err, codes.NotFound, "billing.credits.pack_not_found")

	// Two Starter packs: the price, currency and credits are the pack's, times the quantity.
	c := l.create(&billingv1.CreatePaymentLinkRequest{PackId: starter.GetId(), CustomerExternalId: "cus_a", Quantity: 2, IdempotencyKey: "buy-1"})
	k := c.GetLink()
	if k.GetKind() != "credit_pack" || k.GetAmountMinor() != 1000 || k.GetCurrency() != "EUR" || k.GetCredits() != 2000 || k.GetQuantity() != 2 || k.GetPackId() != starter.GetId() ||
		k.GetDescription() != "Starter" || !k.GetSingleUse() || k.GetCustomerExternalId() != "cus_a" {
		t.Fatalf("link = %v", k)
	}
	// The same key with another quantity is a mistake, not a repeat; with the same request it is a repeat.
	_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{PackId: starter.GetId(), CustomerExternalId: "cus_a", Quantity: 3, IdempotencyKey: "buy-1"})
	keyOf(t, err, codes.AlreadyExists, "billing.payment_link.idempotency_conflict")
	if again := l.create(&billingv1.CreatePaymentLinkRequest{PackId: starter.GetId(), CustomerExternalId: "cus_a", Quantity: 2, IdempotencyKey: "buy-1"}); !again.GetReplayed() || again.GetToken() != "" {
		t.Errorf("repeat = %v", again)
	}

	// Changing the pack's price later does not change what the link frozen: the amount is the one at creation.
	if listed, err := l.svc.ListPaymentLinks(l.mine, &billingv1.ListPaymentLinksRequest{Kind: "credit_pack"}); err != nil || len(listed.GetLinks()) != 1 {
		t.Fatalf("list credit_pack links = %v, %v", listed, err)
	}

	// The hosted page shows what is bought; opening it makes no invoice.
	landing, err := l.page(c.GetToken())
	pk := landing.GetAdhoc().GetPack()
	if err != nil || pk.GetName() != "Starter" || pk.GetCredits() != 2000 || pk.GetQuantity() != 2 || pk.GetValidityDays() != 30 || landing.GetAdhoc().GetAmountMinor() != 1000 {
		t.Fatalf("landing = %v, %v", landing, err)
	}
	if got := balanceOf(t, l.tid, cid); got != 0 {
		t.Fatalf("credits before payment = %d", got)
	}

	child := l.start(c.GetToken())
	page := l.waitState(child, "open")
	if page.GetInvoice().GetTotalMinor() != 1000 || page.GetInvoice().GetLines()[0].GetDescription() != "Starter x2" {
		t.Fatalf("invoice = %v", page.GetInvoice())
	}
	// An open invoice grants nothing.
	time.Sleep(300 * time.Millisecond)
	if got := balanceOf(t, l.tid, cid); got != 0 {
		t.Fatalf("credits for an unpaid invoice = %d", got)
	}

	if r, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "mock"}); err != nil || r.GetAction() != "started" {
		t.Fatalf("pay = %v, %v", r, err)
	}
	l.waitState(child, "paid")

	// Paid: the credits are there, in one lot that expires in 30 days, bought from the pack.
	bal, err := query.BalanceOf(context.Background(), shared.app, l.tid, cid)
	if err != nil || bal.Credits != 2000 || len(bal.Lots) != 1 {
		t.Fatalf("balance = %+v, %v", bal, err)
	}
	lot := bal.Lots[0]
	if lot.Source != "purchase" || lot.Granted != 2000 || lot.ExpiresAt == nil || lot.ExpiresAt.Sub(l.clk.Now().AddDate(0, 0, 30)).Abs() > time.Hour {
		t.Errorf("lot = %+v", lot)
	}
	entries, _, err := query.Ledger(context.Background(), shared.app, l.tid, cid, 10, "")
	if err != nil || len(entries) != 1 || entries[0].Kind != "grant" || entries[0].Source != "purchase" || entries[0].Reason != "credit pack purchase: Starter x2" {
		t.Fatalf("ledger = %+v, %v", entries, err)
	}

	// A duplicate confirmation of the same payment grants nothing more: the provider's webhook again, a replayed settlement, and the grant itself.
	invoiceID := page.GetInvoice().GetId()
	again, err := l.svc.SettleInvoicePayment(l.mine, &billingv1.SettleInvoicePaymentRequest{InvoiceId: invoiceID, Provider: "paddle", ProviderRef: "txn_dup", AmountMinor: 1000, Currency: "EUR"})
	if err != nil || again.GetSettled() {
		t.Errorf("settling a paid invoice = %v, %v", again, err)
	}
	err = shared.system.InTx(context.Background(), l.tid, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return command.PurchasePaid(context.Background(), tx, command.Purchase{TenantID: l.tid, CustomerID: cid, LinkID: uuid.New(), Quantity: 2, Credits: 2000, InvoiceID: uuid.MustParse(invoiceID),
			InvoiceNo: "X", Currency: "EUR", AmountMinor: 1000, Now: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := balanceOf(t, l.tid, cid); got != 2000 {
		t.Errorf("balance after duplicates = %d, want 2000", got)
	}

	// The tenant is told once, with the customer and the new balance.
	bought := l.eventData("credits.purchased")
	if len(bought) != 1 || bought[0]["customer_external_id"] != "cus_a" || bought[0]["credits"] != float64(2000) || bought[0]["balance"] != float64(2000) || bought[0]["pack_id"] != starter.GetId() ||
		bought[0]["invoice_id"] != invoiceID || bought[0]["expires_at"] == nil {
		t.Errorf("credits.purchased = %v", bought)
	}
	if got := l.link(c.GetLink().GetId()); got.GetStatus() != "paid" || got.GetPaidCount() != 1 {
		t.Errorf("link = %v", got)
	}

	// A pack without a validity gives credits that never expire, for a reusable link once per use.
	multi := false
	r := l.create(&billingv1.CreatePaymentLinkRequest{PackId: forever.GetId(), CustomerExternalId: "cus_a", SingleUse: &multi})
	for i := 0; i < 2; i++ {
		ch := l.start(r.GetToken())
		l.waitState(ch, "open")
		if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: ch, Method: "mock"}); err != nil {
			t.Fatal(err)
		}
		l.waitState(ch, "paid")
	}
	bal, _ = query.BalanceOf(context.Background(), shared.app, l.tid, cid)
	if bal.Credits != 2100 || len(bal.Lots) != 3 {
		t.Errorf("after two more purchases: %+v", bal)
	}
	if bal.Lots[len(bal.Lots)-1].ExpiresAt != nil && bal.Lots[0].ExpiresAt == nil {
		t.Errorf("lots out of order: %+v", bal.Lots)
	}
	if n := len(l.eventData("credits.purchased")); n != 3 {
		t.Errorf("credits.purchased x%d, want 3", n)
	}
}

func testAVoidedCreditPurchaseGrantsNothing(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	cid := uuid.MustParse(l.customer("cus_a").Id)
	p := l.pack("Starter", 1000, 500, "USD", 0)
	c := l.create(&billingv1.CreatePaymentLinkRequest{PackId: p.GetId(), CustomerExternalId: "cus_a"})
	child := l.start(c.GetToken())
	l.waitState(child, "open")

	// The tenant revokes the link before the customer pays: the unpaid invoice is voided and nothing is granted.
	if _, err := l.svc.RevokePaymentLink(l.mine, &billingv1.RevokePaymentLinkRequest{LinkId: c.GetLink().GetId()}); err != nil {
		t.Fatal(err)
	}
	var invoiceStatus string
	testkit.Eventually(t, 30*time.Second, "the invoice to be voided", func() bool {
		if err := shared.sql.QueryRow(context.Background(), `SELECT status FROM invoices WHERE tenant_id = $1 AND kind = 'LINK'`, l.tid).Scan(&invoiceStatus); err != nil {
			t.Fatal(err)
		}
		return invoiceStatus == "void"
	})
	if got := balanceOf(t, l.tid, cid); got != 0 {
		t.Errorf("credits for a voided invoice = %d", got)
	}
	if n := len(l.eventData("credits.purchased")); n != 0 {
		t.Errorf("credits.purchased x%d for a voided invoice", n)
	}
	// And the revoked link cannot be started again.
	if _, err := l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: c.GetToken()}); err == nil {
		t.Errorf("a revoked link started: %v", err)
	}
}

func testCreditPacksAreBoughtFromThePortal(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	a, b := uuid.MustParse(l.customer("cus_a").Id), uuid.MustParse(l.customer("cus_b").Id)
	starter := l.pack("Starter", 1000, 500, "USD", 90)
	gone := l.pack("Gone", 5, 5, "USD", 0)
	if err := command.SetPackActive(context.Background(), shared.app, l.tid, uuid.MustParse(gone.GetId()), false); err != nil {
		t.Fatal(err)
	}
	// Some credits the customer already has.
	if _, err := command.Grant(context.Background(), shared.app, l.tid, a, 40, "promo", nil, "welcome", ""); err != nil {
		t.Fatal(err)
	}
	pa, err := l.svc.CreatePortalLink(l.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_a"})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := l.svc.CreatePortalLink(l.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_b"})
	if err != nil {
		t.Fatal(err)
	}

	page, err := l.svc.GetPortal(l.visitor, &billingv1.GetPortalRequest{PortalToken: pa.GetToken()})
	if err != nil || page.GetCreditBalance() != 40 || len(page.GetCreditLots()) != 1 || !page.GetCanBuyCredits() || len(page.GetCreditPacks()) != 1 {
		t.Fatalf("portal = %v, %v", page, err)
	}
	if pk := page.GetCreditPacks()[0]; pk.GetId() != starter.GetId() || pk.GetCredits() != 1000 || pk.GetPriceMinor() != 500 || pk.GetValidityDays() != 90 || pk.GetCurrencyExponent() != 2 {
		t.Errorf("pack on the portal = %v", pk)
	}
	if other, _ := l.svc.GetPortal(l.visitor, &billingv1.GetPortalRequest{PortalToken: pb.GetToken()}); other.GetCreditBalance() != 0 || len(other.GetCreditLots()) != 0 {
		t.Errorf("another customer's portal shows %v credits", other.GetCreditBalance())
	}

	buy := func(token, pack string, qty int32) (string, error) {
		r, err := l.svc.StartPortalPurchase(l.visitor, &billingv1.StartPortalPurchaseRequest{PortalToken: token, PackId: pack, Quantity: qty})
		return r.GetInvoiceToken(), err
	}
	if _, err := buy("nope-0123456789abcdefghijklmnopqrstuvwxyz", starter.GetId(), 1); status.Code(err) != codes.NotFound {
		t.Errorf("an unknown portal: %v", err)
	}
	_, err = buy(pa.GetToken(), gone.GetId(), 1)
	keyOf(t, err, codes.FailedPrecondition, "billing.credits.pack_inactive")
	_, err = buy(pa.GetToken(), uuid.NewString(), 1)
	keyOf(t, err, codes.NotFound, "billing.credits.pack_not_found")
	_, err = buy(pa.GetToken(), "not-a-uuid", 1)
	keyOf(t, err, codes.NotFound, "billing.credits.pack_not_found")
	_, err = buy(pa.GetToken(), starter.GetId(), 5000)
	keyOf(t, err, codes.InvalidArgument, "billing.credits.bad_quantity")

	first, err := buy(pa.GetToken(), starter.GetId(), 0)
	if err != nil {
		t.Fatal(err)
	}
	open := l.waitState(first, "open")
	// Pressing the button again continues with the same invoice: no second number is burned.
	second, err := buy(pa.GetToken(), starter.GetId(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if again := l.waitState(second, "open"); again.GetInvoice().GetId() != open.GetInvoice().GetId() {
		t.Errorf("the second press made invoice %s, want %s", again.GetInvoice().GetId(), open.GetInvoice().GetId())
	}
	if open.GetInvoice().GetTotalMinor() != 500 || open.GetInvoice().GetKind() != "LINK" {
		t.Fatalf("invoice = %v", open.GetInvoice())
	}
	if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: first, Method: "mock"}); err != nil {
		t.Fatal(err)
	}
	l.waitState(first, "paid")

	// The credits go to the portal's customer, nobody else.
	if got := balanceOf(t, l.tid, a); got != 1040 {
		t.Errorf("customer a has %d, want 1040", got)
	}
	if got := balanceOf(t, l.tid, b); got != 0 {
		t.Errorf("customer b has %d, want 0", got)
	}
	page, _ = l.svc.GetPortal(l.visitor, &billingv1.GetPortalRequest{PortalToken: pa.GetToken()})
	if page.GetCreditBalance() != 1040 || len(page.GetCreditLots()) != 2 || page.GetCreditLots()[0].GetExpireTime() == nil {
		t.Errorf("portal after the purchase = %v", page)
	}
	// A paid purchase is done: the next press makes a new invoice.
	third, err := buy(pa.GetToken(), starter.GetId(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if next := l.waitState(third, "open"); next.GetInvoice().GetId() == open.GetInvoice().GetId() {
		t.Error("a paid purchase was offered again")
	}
}

func testCreditsLowIsAnnouncedOnceWhenTheBalanceCrossesTheThreshold(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	cid := l.customer("cus_a").Id
	ctx := l.mine
	cs, err := l.svc.GetCreditSettings(ctx, &billingv1.GetCreditSettingsRequest{})
	if err != nil || cs.GetLowThreshold() != 0 {
		t.Fatalf("default settings = %v, %v", cs, err)
	}
	_, err = l.svc.SetCreditSettings(ctx, &billingv1.SetCreditSettingsRequest{LowThreshold: -1})
	keyOf(t, err, codes.InvalidArgument, "billing.credits.bad_threshold")
	if _, err := l.svc.GrantCredits(ctx, &billingv1.GrantCreditsRequest{CustomerId: cid, Credits: 150, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	consume := func(n int64, key string) {
		t.Helper()
		if _, err := l.svc.ConsumeCredits(ctx, &billingv1.ConsumeCreditsRequest{CustomerId: cid, Credits: n, IdempotencyKey: key, CustomerExternalId: "cus_a"}); err != nil {
			t.Fatal(err)
		}
	}
	// Off by default: crossing nothing announces nothing.
	consume(10, "c0") // 140
	if n := len(l.eventData("credits.low")); n != 0 {
		t.Fatalf("credits.low x%d while off", n)
	}

	if r, err := l.svc.SetCreditSettings(ctx, &billingv1.SetCreditSettingsRequest{LowThreshold: 100}); err != nil || r.GetLowThreshold() != 100 {
		t.Fatalf("set = %v, %v", r, err)
	}
	if cs, _ := l.svc.GetCreditSettings(ctx, &billingv1.GetCreditSettingsRequest{}); cs.GetLowThreshold() != 100 {
		t.Fatalf("settings = %v", cs)
	}
	consume(30, "c1") // 110: still above
	if n := len(l.eventData("credits.low")); n != 0 {
		t.Fatalf("credits.low x%d above the threshold", n)
	}
	consume(20, "c2") // 90: crossed
	consume(20, "c2") // a retry of the same call
	consume(10, "c3") // 80: already below
	low := l.eventData("credits.low")
	if len(low) != 1 || low[0]["customer_id"] != cid || low[0]["customer_external_id"] != "cus_a" || low[0]["balance"] != float64(90) || low[0]["threshold"] != float64(100) {
		t.Fatalf("credits.low = %v", low)
	}

	// Topping up and falling again announces again.
	if _, err := l.svc.GrantCredits(ctx, &billingv1.GrantCreditsRequest{CustomerId: cid, Credits: 100, Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	consume(100, "c4") // 180 -> 80
	if n := len(l.eventData("credits.low")); n != 2 {
		t.Errorf("credits.low x%d, want 2", n)
	}
	// A refused consume (too few credits) announces nothing and takes nothing.
	if _, err := l.svc.ConsumeCredits(ctx, &billingv1.ConsumeCreditsRequest{CustomerId: cid, Credits: 5000, IdempotencyKey: "c5"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("an overdraft: %v", err)
	}
	if n := len(l.eventData("credits.low")); n != 2 {
		t.Errorf("credits.low x%d after a refused consume", n)
	}

	// Another tenant's threshold is its own.
	other := newLinkEnv(t, envOpts{sandbox: true}, false)
	if cs, _ := other.svc.GetCreditSettings(other.mine, &billingv1.GetCreditSettingsRequest{}); cs.GetLowThreshold() != 0 {
		t.Errorf("another tenant has the threshold %d", cs.GetLowThreshold())
	}
}

func testCreditPackValidityIsValidated(t *testing.T) {
	infra(t)
	ctx, tid := context.Background(), uuid.New()
	for _, days := range []int32{-1, 3651} {
		_, err := command.CreatePack(ctx, shared.app, tid, "P", 10, 10, "USD", days)
		wantKey(t, err, codes.InvalidArgument, "billing.credits.bad_validity")
	}
	p, err := command.CreatePack(ctx, shared.app, tid, "P", 10, 10, "USD", 3650)
	if err != nil || p.ValidityDays == nil || *p.ValidityDays != 3650 {
		t.Fatalf("pack = %+v, %v", p, err)
	}
	got, err := query.PackByID(ctx, shared.app, tid, p.ID)
	if err != nil || got.ValidityDays == nil || *got.ValidityDays != 3650 {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	forever, _ := command.CreatePack(ctx, shared.app, tid, "F", 10, 10, "USD", 0)
	if forever.ValidityDays != nil {
		t.Errorf("a pack with no validity has %v", *forever.ValidityDays)
	}
}
