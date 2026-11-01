// Package conformance is the test every payments.Provider adapter must pass (docs/payment-provider-contract.md). An adapter's own tests supply a Harness: a way to
// build the adapter against an httptest fake that behaves as a Scenario says, and the notifications the adapter's provider would send. Run then checks what
// payments-worker relies on, whatever the provider:
//
//   - a charge that succeeds, one that is declined, a provider that fails (5xx, 429) and one that does not answer: ErrTimeout (the outcome is unknown, never a decline);
//   - the idempotency key reaches the provider on every charge and refund, and the Capabilities that claim idempotency are not lying;
//   - a refund, a partial refund that refunds the amount asked for and not the whole payment, or is refused;
//   - a duplicate notification is the same notification, a forged one is payments.ErrUnverified;
//   - the Capabilities match what the adapter implements (a hosted checkout, a renewal the provider owns).
//
// It runs against fakes, so a pass says the adapter handles what its provider's documentation says, not that the provider does: the sandbox test of each adapter
// is the other half.
package conformance

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Fixed values every fake and every notification use, so the suite can ask for them back.
const (
	// ChargeKey is the idempotency key of the charge the suite makes: an invoice "inv-1" and its first attempt, in the shape payments-worker sends.
	ChargeKey = "inv-1:charge:1"
	// InvoiceID is the invoice of ChargeKey.
	InvoiceID = "inv-1"
	// Total is the amount, in minor units, of the charge and of the payment that is refunded.
	Total int64 = 1116
	// Part is the amount of a partial refund; it is less than Total.
	Part int64 = 400
)

// Scenario is how the fake provider behaves.
type Scenario string

const (
	// ChargeSucceeds: the provider takes the payment (and refunds it, whole or in part, when asked).
	ChargeSucceeds Scenario = "charge succeeds"
	// ChargeDeclined: the provider refuses the card.
	ChargeDeclined Scenario = "charge declined"
	// ServerError: the provider answers 500 to everything.
	ServerError Scenario = "server error"
	// RateLimited: the provider answers 429 to everything.
	RateLimited Scenario = "rate limited"
	// NeverAnswers: the provider does not answer within the client's timeout. The Provider the harness builds for it must have a short one.
	NeverAnswers Scenario = "never answers"
)

// WebhookCase is which notification the harness builds.
type WebhookCase string

const (
	// WebhookSucceeded: a genuine notification that the charge ChargeKey succeeded.
	WebhookSucceeded WebhookCase = "succeeded"
	// WebhookDeclined: a genuine notification that the charge ChargeKey was declined.
	WebhookDeclined WebhookCase = "declined"
	// WebhookForged: a notification that does not come from the provider (a wrong signature; for a provider that is asked, one the provider does not recognise).
	WebhookForged WebhookCase = "forged"
)

// Probe records what the fake provider saw, so the suite can check it. A fake calls Charge and Refund once per request that creates one.
type Probe struct {
	mu      sync.Mutex
	charges []string
	refunds []SeenRefund
}

// SeenRefund is a refund as the provider received it.
type SeenRefund struct {
	// Key is the idempotency key the request carried; empty when the adapter sent none.
	Key string
	// AmountMinor is what the provider would refund: the amount asked for, or the whole payment (Total) when the request named none.
	AmountMinor int64
}

// Charge records a charge request that carried key (empty if it carried none).
func (p *Probe) Charge(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.charges = append(p.charges, key)
}

// Refund records a refund request.
func (p *Probe) Refund(r SeenRefund) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refunds = append(p.refunds, r)
}

func (p *Probe) seenCharges() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.charges...)
}

func (p *Probe) seenRefunds() []SeenRefund {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SeenRefund(nil), p.refunds...)
}

// Harness is what an adapter supplies.
type Harness struct {
	// Provider builds the adapter against a fake that behaves as sc says.
	Provider func(t *testing.T, sc Scenario) (payments.Provider, *Probe)
	// Currency, PaymentMethod and ChargeRef are what a charge and a refund of Total are made with: a currency the adapter charges in, the customer's payment
	// method in the form the adapter wants, and the id its Charge returns, which Refund takes (the "charge id").
	Currency      string
	PaymentMethod string
	ChargeRef     string
	// RefundsByInvoice: the provider refunds against the invoice, not a charge id (mock-psp), so a refund without the charge id is not refused.
	RefundsByInvoice bool
	// SettlesLater: Charge does not report the outcome but payments.PendingError, and the notification settles it (a merchant of record).
	SettlesLater bool
	// PartialRefunds: a refund of Part is made as a partial refund. When false, it must be refused, and never turned into a refund of the whole payment.
	PartialRefunds bool
	// Webhook builds a provider and a notification of the case, for an adapter that is a payments.Notifier. Nil for one that is not.
	Webhook func(t *testing.T, c WebhookCase) (p payments.Provider, body []byte, header http.Header)
	// Parse is the adapter's payments.ParseNotification, registered for its webhooks. Nil when Webhook is.
	Parse payments.ParseNotification
	// Skip says which scenarios the adapter cannot show and why (a provider that settles a charge later cannot decline it in the answer).
	Skip map[Scenario]string
}

func (h Harness) request() payments.ChargeRequest {
	return payments.ChargeRequest{IdempotencyKey: ChargeKey, InvoiceID: InvoiceID, AmountMinor: Total, Currency: h.Currency, PaymentMethod: h.PaymentMethod}
}

func (h Harness) refund(amount int64, key string) payments.RefundRequest {
	return payments.RefundRequest{IdempotencyKey: key, InvoiceID: InvoiceID, ChargeID: h.ChargeRef, AmountMinor: amount, Currency: h.Currency}
}

func (h Harness) scenario(t *testing.T, sc Scenario, f func(t *testing.T, p payments.Provider, probe *Probe)) {
	t.Run(string(sc), func(t *testing.T) {
		if why, skip := h.Skip[sc]; skip {
			t.Skip(why)
		}
		p, probe := h.Provider(t, sc)
		f(t, p, probe)
	})
}

// Run runs the suite.
func Run(t *testing.T, h Harness) {
	t.Helper()
	ctx := context.Background()

	t.Run("capabilities match the adapter", func(t *testing.T) {
		p, _ := h.Provider(t, ChargeSucceeds)
		caps := p.Capabilities()
		if caps.Name == "" || caps.Name != p.Name() {
			t.Errorf("Capabilities().Name = %q, Name() = %q", caps.Name, p.Name())
		}
		if !caps.CanCharge(h.Currency) {
			t.Errorf("the suite charges in %s, which the adapter's currencies %v do not include", h.Currency, caps.Currencies)
		}
		if caps.OwnsDunning && !caps.OwnsRenewal {
			t.Error("OwnsDunning without OwnsRenewal: the provider retries renewals it does not schedule")
		}
		_, ownsCheckout := p.(payments.CheckoutProvider)
		_, ownsInvoiceCheckout := p.(payments.InvoiceCheckoutProvider)
		if caps.HostedCheckout != (ownsCheckout || ownsInvoiceCheckout) {
			t.Errorf("HostedCheckout = %v, but the adapter implements CheckoutProvider = %v, InvoiceCheckoutProvider = %v", caps.HostedCheckout, ownsCheckout, ownsInvoiceCheckout)
		}
		if _, ok := p.(payments.SubscriptionManager); caps.OwnsRenewal && !ok {
			t.Error("OwnsRenewal, but the adapter cannot cancel the subscription it runs (payments.SubscriptionManager)")
		}
		_, isNotifier := p.(payments.Notifier)
		if isNotifier != (h.Webhook != nil) || (h.Webhook != nil) != (h.Parse != nil) {
			t.Errorf("Notifier = %v, Harness.Webhook set = %v, Harness.Parse set = %v: an adapter that confirms notifications supplies both", isNotifier, h.Webhook != nil, h.Parse != nil)
		}
	})

	h.scenario(t, ChargeSucceeds, func(t *testing.T, p payments.Provider, probe *Probe) {
		res, err := p.Charge(ctx, h.request())
		var pending *payments.PendingError
		switch {
		case h.SettlesLater:
			if !errors.As(err, &pending) {
				t.Fatalf("Charge = %+v, %v; a provider that settles later answers payments.PendingError", res, err)
			}
		case err != nil:
			t.Fatalf("Charge: %v", err)
		case res.ChargeID == "":
			t.Fatal("Charge succeeded without a charge id (a refund needs it)")
		}
		keys := probe.seenCharges()
		if len(keys) != 1 || keys[0] != ChargeKey {
			t.Errorf("the provider saw the idempotency keys %q for one charge; want exactly %q (it is how the notification and Status find the charge again)", keys, ChargeKey)
		}
		if _, err := p.Charge(ctx, payments.ChargeRequest{AmountMinor: Total, Currency: h.Currency, PaymentMethod: h.PaymentMethod}); err == nil {
			t.Error("a charge without an idempotency key was sent")
		}
		if len(probe.seenCharges()) != 1 {
			t.Error("a charge without an idempotency key reached the provider")
		}
	})

	h.scenario(t, ChargeDeclined, func(t *testing.T, p payments.Provider, _ *Probe) {
		_, err := p.Charge(ctx, h.request())
		var d *payments.DeclinedError
		if !errors.As(err, &d) || d.Code == "" {
			t.Fatalf("Charge = %v; a refused card is a *payments.DeclinedError with a code", err)
		}
	})

	for _, sc := range []Scenario{ServerError, RateLimited} {
		h.scenario(t, sc, func(t *testing.T, p payments.Provider, _ *Probe) {
			_, err := p.Charge(ctx, h.request())
			var tr *payments.TransientError
			var d *payments.DeclinedError
			if !errors.As(err, &tr) || errors.As(err, &d) {
				t.Fatalf("Charge = %v; a provider that fails is a *payments.TransientError (retry), not a decline", err)
			}
		})
	}

	h.scenario(t, NeverAnswers, func(t *testing.T, p payments.Provider, _ *Probe) {
		_, err := p.Charge(ctx, h.request())
		var tr *payments.TransientError
		var d *payments.DeclinedError
		if !errors.Is(err, payments.ErrTimeout) || errors.As(err, &tr) || errors.As(err, &d) {
			t.Fatalf("Charge = %v; no answer means the outcome is unknown: payments.ErrTimeout, which is neither retried blindly nor a decline", err)
		}
		// Whatever Status says about a charge it could not ask about, it does not say it never happened.
		if st, err := p.Status(ctx, ChargeKey); err == nil || st.State == payments.StateNotFound || st.State == payments.StateSucceeded {
			t.Errorf("Status with no answer = %+v, %v; want an error", st, err)
		}
	})

	t.Run("refund", func(t *testing.T) {
		p, probe := h.Provider(t, ChargeSucceeds)
		if !p.Capabilities().Refunds {
			t.Skip("the provider has no refund API (Capabilities.Refunds)")
		}
		res, err := p.Refund(ctx, h.refund(Total, "inv-1:refund:1"))
		if err != nil || res.RefundID == "" {
			t.Fatalf("Refund = %+v, %v", res, err)
		}
		seen := probe.seenRefunds()
		if len(seen) != 1 || seen[0].AmountMinor != Total {
			t.Fatalf("the provider saw refunds %+v; want one of %d", seen, Total)
		}
		if p.Capabilities().IdempotentRefunds && seen[0].Key != "inv-1:refund:1" {
			t.Errorf("IdempotentRefunds, but the refund carried the key %q", seen[0].Key)
		}
		if _, err := p.Refund(ctx, payments.RefundRequest{IdempotencyKey: "k", Currency: h.Currency, AmountMinor: Total}); err == nil && !h.RefundsByInvoice {
			t.Error("a refund without the charge id was accepted")
		}
	})

	t.Run("partial refund", func(t *testing.T) {
		p, probe := h.Provider(t, ChargeSucceeds)
		if !p.Capabilities().Refunds {
			t.Skip("the provider has no refund API")
		}
		res, err := p.Refund(ctx, h.refund(Part, "inv-1:refund:2"))
		seen := probe.seenRefunds()
		if h.PartialRefunds {
			if err != nil || res.RefundID == "" {
				t.Fatalf("Refund of %d of %d = %+v, %v", Part, Total, res, err)
			}
			if len(seen) != 1 || seen[0].AmountMinor != Part {
				t.Fatalf("the provider saw refunds %+v; want one of %d, not of the whole payment", seen, Part)
			}
			return
		}
		if err == nil {
			t.Error("a partial refund the adapter cannot make was accepted")
		}
		for _, r := range seen {
			if r.AmountMinor != Part {
				t.Errorf("a refund of %d was asked for and the provider refunded %d", Part, r.AmountMinor)
			}
		}
	})

	if h.Webhook == nil {
		return
	}
	t.Run("notifications", func(t *testing.T) {
		for _, c := range []WebhookCase{WebhookSucceeded, WebhookDeclined} {
			t.Run(string(c), func(t *testing.T) {
				p, body, header := h.Webhook(t, c)
				n := p.(payments.Notifier)
				first, err := n.ConfirmNotification(ctx, body, header)
				if err != nil {
					t.Fatalf("ConfirmNotification: %v", err)
				}
				want := payments.StateSucceeded
				if c == WebhookDeclined {
					want = payments.StateDeclined
				}
				if first.Kind != payments.KindCharge || first.State != want || first.Ref.IdempotencyKey != ChargeKey || first.Ref.EventID == "" {
					t.Fatalf("notification = %+v; want a %s charge of %q with an event id", first, want, ChargeKey)
				}
				if c == WebhookSucceeded && first.ProviderRef == "" {
					t.Error("a succeeded charge without the provider's reference")
				}
				if c == WebhookDeclined && first.DeclineCode == "" {
					t.Error("a declined charge without a decline code")
				}
				// A duplicate delivery is the same notification, so the worker can recognise it by the event id.
				again, err := n.ConfirmNotification(ctx, body, header)
				if err != nil || !reflect.DeepEqual(first, again) {
					t.Errorf("a duplicate delivery gave %+v, %v; want the notification it gave the first time", again, err)
				}
				ref, err := h.Parse(body, header)
				if err != nil || ref.EventID != first.Ref.EventID {
					t.Errorf("Parse = %+v, %v; want the event id %q", ref, err, first.Ref.EventID)
				}
			})
		}
		t.Run(string(WebhookForged), func(t *testing.T) {
			p, body, header := h.Webhook(t, WebhookForged)
			if n, err := p.(payments.Notifier).ConfirmNotification(ctx, body, header); !errors.Is(err, payments.ErrUnverified) {
				t.Fatalf("ConfirmNotification of a forged notification = %+v, %v; want payments.ErrUnverified", n, err)
			}
		})
	})
}
