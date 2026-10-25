//go:build integration

package itest

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// testReferralPassedOn: the referral cookie a visitor brings to a hosted page reaches billing-svc with the request that starts a payment, and billing passes it on to
// the affiliate program for the customer who is paying. A guest has nobody to attribute. A program that fails, or is slow, never fails the payment.
func testReferralPassedOn(t *testing.T) {
	l := newLinkEnv(t, envOpts{sandbox: true}, false)
	worker := l.startWorkers(nil)
	_ = worker
	taken := func() []*plansv1.AttributeCustomerRequest {
		l.w.mu.Lock()
		defer l.w.mu.Unlock()
		return append([]*plansv1.AttributeCustomerRequest(nil), l.w.referrals...)
	}
	touched := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	ref := func(token string) *billingv1.StartLinkPaymentRequest {
		return &billingv1.StartLinkPaymentRequest{Token: token, ReferralCode: "ANNPARTNER", ReferralTouchTime: timestamppb.New(touched)}
	}

	// A link made out to a customer: the customer is attributed when the payer presses Continue, and again when they pay (first touch wins there; billing asks every time).
	single := false
	c := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 2500, Description: "Workshop", CustomerExternalId: "cus_a", SingleUse: &single})
	r, err := l.svc.StartLinkPayment(l.visitor, ref(c.GetToken()))
	if err != nil {
		t.Fatal(err)
	}
	got := taken()
	if len(got) != 1 || got[0].GetCustomerId() != l.customer("cus_a").Id || got[0].GetCode() != "ANNPARTNER" || got[0].GetSource() != "link" || !got[0].GetTouchTime().AsTime().Equal(touched) {
		t.Fatalf("after Continue: %v", got)
	}
	child := r.GetInvoiceToken()
	l.waitState(child, "open")
	if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: child, Method: "mock", ReferralCode: "ANNPARTNER", ReferralTouchTime: timestamppb.New(touched)}); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 2 || got[1].GetCustomerId() != l.customer("cus_a").Id {
		t.Fatalf("after paying: %v", got)
	}
	l.waitState(child, "paid")

	// Nothing to pass on without a cookie; nobody to attribute for a guest.
	if _, err := l.svc.StartLinkPayment(l.visitor, &billingv1.StartLinkPaymentRequest{Token: c.GetToken()}); err != nil {
		t.Fatal(err)
	}
	guest := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 900, Description: "Donation"})
	if _, err := l.svc.StartLinkPayment(l.visitor, ref(guest.GetToken())); err != nil {
		t.Fatal(err)
	}
	if got := taken(); len(got) != 2 {
		t.Fatalf("a request without a cookie or for a guest asked the program: %v", got)
	}

	// The program is down: the payment goes through all the same.
	l.w.mu.Lock()
	l.w.referralErr = errors.New("affiliate program unavailable")
	l.w.mu.Unlock()
	other := l.create(&billingv1.CreatePaymentLinkRequest{Kind: "adhoc", AmountMinor: 1200, Description: "Retry", CustomerExternalId: "cus_b"})
	r2, err := l.svc.StartLinkPayment(l.visitor, ref(other.GetToken()))
	if err != nil {
		t.Fatalf("a failing affiliate program failed the payment: %v", err)
	}
	l.waitState(r2.GetInvoiceToken(), "open")
	if _, err := l.svc.RequestHostedPayment(l.visitor, &billingv1.RequestHostedPaymentRequest{Token: r2.GetInvoiceToken(), Method: "mock", ReferralCode: "ANNPARTNER"}); err != nil {
		t.Fatalf("a failing affiliate program failed the pay request: %v", err)
	}
	l.waitState(r2.GetInvoiceToken(), "paid")
	if got := taken(); len(got) != 4 {
		t.Fatalf("both requests should still have asked: %v", got)
	}
}
