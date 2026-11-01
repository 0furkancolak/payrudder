//go:build integration

package itest

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/stubworker"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
)

// Every path that takes a payment asks the one gate (pkg/paymentgate): a tenant on hold takes none; a customer blocked for fraud or chargeback pays no invoice that is
// open; a customer blocked for another reason still pays what was invoiced before; and a link for a blocked customer is not made. docs/plans/13 has the table.
func testTheGateRefusesPayments(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	l.startWorkers(nil)
	refused := func(what string, err error, key string) {
		t.Helper()
		if status.Code(err) != codes.FailedPrecondition || errkey.Of(err) != key {
			t.Errorf("%s: %v (key %q), want FailedPrecondition %s", what, err, errkey.Of(err), key)
		}
	}
	pay := func(token string) error {
		_, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: token, Method: "mock"})
		return err
	}
	block := func(category string) {
		l.customer("cus_a").BlockTime, l.customer("cus_a").BlockCategory = timestamppb.Now(), category
	}
	lift := func() {
		l.customer("cus_a").BlockTime, l.customer("cus_a").BlockCategory, l.customer("cus_a").BlockedUntil = nil, "", nil
	}
	hold := func(s plansv1.TenantStatus) { l.w.tenant.Status = s }

	link := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 1000, Description: "Workshop", CustomerExternalId: "cus_a"})
	child := l.start(link.GetToken())
	l.waitState(child, "open")
	if p, _ := l.page(child); !p.GetPayable() {
		t.Fatal("an open invoice must be payable before anything is held")
	}

	t.Run("an invoice that is open, for a customer blocked for fraud or chargeback", func(t *testing.T) {
		for _, category := range []string{"fraud", "chargeback"} {
			block(category)
			if p, _ := l.page(child); p.GetPayable() {
				t.Errorf("%s: the page offers a payment that would be refused", category)
			}
			refused(category+": pay", pay(child), "billing.customer.blocked")
		}
		lift()
	})

	t.Run("a tenant on hold takes no payment, on a page, on a link or for a new link", func(t *testing.T) {
		fresh := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 500, Description: "Setup"})
		for _, st := range []plansv1.TenantStatus{plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, plansv1.TenantStatus_TENANT_STATUS_FROZEN, plansv1.TenantStatus_TENANT_STATUS_BANNED} {
			hold(st)
			if p, _ := l.page(child); p.GetPayable() {
				t.Errorf("%v: the page offers a payment", st)
			}
			refused(st.String()+": pay an invoice", pay(child), "billing.tenant.on_hold")
			_, err := l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: fresh.GetToken()})
			refused(st.String()+": start a link", err, "billing.tenant.on_hold")
			_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "No"})
			refused(st.String()+": make a link", err, "billing.tenant.on_hold")
		}
		var invoices int
		if err := shared.sql.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE tenant_id = $1 AND kind = 'LINK'`, l.tid).Scan(&invoices); err != nil || invoices != 1 {
			t.Fatalf("link invoices = %d (%v): a refused start must make none", invoices, err)
		}
		hold(plansv1.TenantStatus_TENANT_STATUS_ACTIVE)
		if _, err := l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "Yes"}); err != nil {
			t.Errorf("after reinstating: %v", err)
		}
	})

	t.Run("a link is not made for a blocked customer, and a portal offers them no purchase", func(t *testing.T) {
		p := l.pack("Starter", 1000, 500, "USD", 0)
		portal, err := l.svc.CreatePortalLink(l.mine, &billingv1.CreatePortalLinkRequest{CustomerExternalId: "cus_a"})
		if err != nil {
			t.Fatal(err)
		}
		if page, err := l.svc.GetPortal(l.visitor, &billingv1.GetPortalRequest{PortalToken: portal.GetToken()}); err != nil || !page.GetCanBuyCredits() {
			t.Fatalf("portal of a customer in good standing: %v, %v", page, err)
		}
		block("abuse")
		_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "No", CustomerExternalId: "cus_a"})
		refused("an ad-hoc link for a blocked customer", err, "billing.customer.blocked")
		_, err = l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "credit_pack", PackId: p.GetId(), CustomerExternalId: "cus_a"})
		refused("a credit-pack link for a blocked customer", err, "billing.customer.blocked")
		if _, err := l.svc.CreatePaymentLink(l.mine, &billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 100, Description: "Anyone"}); err != nil {
			t.Errorf("a link for nobody in particular: %v", err)
		}
		if page, err := l.svc.GetPortal(l.visitor, &billingv1.GetPortalRequest{PortalToken: portal.GetToken()}); err != nil || page.GetCanBuyCredits() {
			t.Errorf("the portal of a blocked customer offers a purchase: %v, %v", page, err)
		}
		_, err = l.svc.StartPortalPurchase(l.visitor, &billingv1.StartPortalPurchaseRequest{PortalToken: portal.GetToken(), PackId: p.GetId(), Quantity: 1})
		refused("a portal purchase", err, "billing.customer.blocked")
		lift()
	})

	t.Run("a block that has run out, or is for another reason, still lets an issued invoice be paid", func(t *testing.T) {
		// The block ended an hour ago: it holds no more, whatever its category says.
		l.customer("cus_a").BlockTime, l.customer("cus_a").BlockCategory = timestamppb.New(time.Now().Add(-2*time.Hour)), "fraud"
		l.customer("cus_a").BlockedUntil = timestamppb.New(time.Now().Add(-time.Hour))
		if p, _ := l.page(child); !p.GetPayable() {
			t.Error("a block that ran out still hides the payment")
		}
		lift()
		block("abuse")
		if p, _ := l.page(child); !p.GetPayable() {
			t.Error("a block for abuse hides an invoice that was issued before it")
		}
		if err := pay(child); err != nil {
			t.Fatalf("paying an issued invoice of a customer blocked for abuse: %v", err)
		}
		l.waitState(child, "paid")
	})
}

// A tenant the operator has taken out of service is charged nothing by dunning: the retry is put off (not given up) and made once the tenant is back.
func testDunningWaitsForATenantOnHold(t *testing.T) {
	e := newEnv(t, envOpts{})
	stub := e.startWorkers(func(c *commandsv1.CommandEnvelope, _ int) stubworker.Action {
		if stubworker.Kind(c) == "charge" {
			return stubworker.Action{Reply: stubworker.Declined}
		}
		return stubworker.Action{Reply: stubworker.Succeed}
	})
	e.addUsage("2026-10", "cus_a", "gb_hours", "1.000", 3)
	e.driveRun("2026-10")
	testkit.Eventually(t, 20*time.Second, "first decline", func() bool {
		s := e.sagaStates("2026-10")
		return s["cus_a"] == "PAYMENT_DECLINED" && s["cus_b"] == "PAYMENT_DECLINED"
	})
	var onHold atomic.Bool
	eng, err := sagacommand.NewEngine(sagacommand.Config{Now: e.clk.Now, StepTimeout: time.Hour, Log: testkit.Logger(),
		Hold: func(_ context.Context, id uuid.UUID) (bool, error) { return id == e.tid && onHold.Load(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	charges := e.count(stub, "charge")

	onHold.Store(true)
	e.clk.Advance(24*time.Hour + time.Minute)
	if res, err := eng.Sweep(context.Background(), shared.system, 100); err != nil || res.Held < 2 {
		t.Fatalf("a pass while on hold: %+v, %v; want at least our 2 retries put off", res, err)
	}
	time.Sleep(500 * time.Millisecond) // a command the pass should not have made would be on its way
	if n := e.count(stub, "charge"); n != charges {
		t.Fatalf("charge commands = %d, want %d: a tenant on hold was charged", n, charges)
	}
	if s := e.sagaStates("2026-10"); s["cus_a"] != "PAYMENT_DECLINED" || s["cus_b"] != "PAYMENT_DECLINED" {
		t.Fatalf("the retry was given up instead of put off: %v", s)
	}

	onHold.Store(false)
	e.clk.Advance(sagacommand.DunningHold + time.Minute)
	if res, err := eng.Sweep(context.Background(), shared.system, 100); err != nil || res.Dunned < 2 {
		t.Fatalf("a pass after the tenant is back: %+v, %v; want our 2 retries", res, err)
	}
	testkit.Eventually(t, 20*time.Second, "the retries are charged", func() bool { return e.count(stub, "charge") == charges+2 })
}
