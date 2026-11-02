package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// vetting is a provider that vets the payer (what iyzico is): Capabilities.NeedsBuyer.
type vetting struct{ fakeReal }

func (*vetting) Capabilities() payments.Capabilities {
	return payments.Capabilities{Name: "real", Refunds: true, NeedsBuyer: true}
}

type customers struct {
	c     *plansv1.Customer
	err   error
	asked []string
}

func (r *customers) GetCustomer(_ context.Context, _ uuid.UUID, ext string) (*plansv1.Customer, error) {
	r.asked = append(r.asked, ext)
	return r.c, r.err
}

func TestAChargeCarriesTheCustomerOnlyToAProviderThatVetsThePayer(t *testing.T) {
	cs := &customers{c: &plansv1.Customer{Id: "c1", Name: "Ada Lovelace", Email: "ada@example.com", Phone: "+905321234567", TaxId: "11111111110",
		Address: &plansv1.Address{Line1: "Nisantasi 1", City: "Istanbul", Country: "TR"}}}
	h := &Handler{Customers: cs}
	ch := &commandsv1.ChargePayment{InvoiceId: uuid.NewString(), IdempotencyKey: "k", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", CustomerExternalId: "ext-1"}
	run := commandRun{tenant: uuid.New()}

	req, err := h.chargeRequest(context.Background(), run, ch, "k", &vetting{})
	if err != nil || req.Buyer == nil || req.Buyer.Phone != "+905321234567" || req.Buyer.TaxID != "11111111110" || req.Buyer.City != "Istanbul" || req.Buyer.IP != "" {
		t.Fatalf("= %+v, %v; the customer's own details, and no payer address for a charge nobody is present for", req, err)
	}
	if len(cs.asked) != 1 || cs.asked[0] != "ext-1" {
		t.Errorf("asked for %v", cs.asked)
	}
	// A provider that does not ask is not sent the customer, and plans-svc is not asked.
	req, err = h.chargeRequest(context.Background(), run, ch, "k", &fakeReal{})
	if err != nil || req.Buyer != nil || len(cs.asked) != 1 {
		t.Errorf("a plain provider: %+v, %v, asked %v", req, err, cs.asked)
	}
	// A customer record that cannot be read retries the charge; it does not decline the card.
	cs.err = errors.New("plans-svc is down")
	if _, err := h.chargeRequest(context.Background(), run, ch, "k", &vetting{}); err == nil {
		t.Error("an unreadable customer must fail the command so that it is retried")
	}
	// No reader (plans-svc not configured) sends no buyer, and the adapter says what it lacks.
	if req, err := (&Handler{}).chargeRequest(context.Background(), run, ch, "k", &vetting{}); err != nil || req.Buyer != nil {
		t.Errorf("no reader: %+v, %v", req, err)
	}
}

func TestACustomerTheProviderCannotTakeDeclinesTheChargeWithTheFields(t *testing.T) {
	h := &Handler{}
	reply, err := h.chargeOutcome(context.Background(), store.LedgerEntry{}, payments.ChargeResult{}, &payments.BuyerIncompleteError{Provider: "iyzico", Missing: []string{"phone", "tax_id"}})
	d := reply.GetPaymentDeclined()
	if err != nil || d.GetDeclineCode() != "buyer_incomplete:phone,tax_id" {
		t.Fatalf("= %v, %v; the provider was not asked: the invoice goes on to the retries of dunning, which read the customer again", reply, err)
	}
}
