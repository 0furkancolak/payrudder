package payments

import "context"

// CheckoutInvoiceRequest asks a provider for a hosted page where a customer pays one invoice (a payment link, phase 34). Unlike CheckoutRequest it
// sells no catalogue price: the amount is the invoice total.
type CheckoutInvoiceRequest struct {
	// IdempotencyKey identifies the charge (link:{invoice}:{n}); the provider hands it back in its notification, which is how the payment finds its invoice.
	IdempotencyKey string
	InvoiceID      string
	Number         string
	Description    string
	AmountMinor    int64
	Currency       string
	CustomerEmail  string
	CustomerName   string
	// ReturnURL is where the provider sends the customer afterwards. It only shows state: the notification settles the invoice.
	ReturnURL string
	// CallbackURL, when set, is where a provider that posts the result of the payment through the payer's browser (iyzico) posts it: a route of Payrudder's that
	// confirms the payment with the provider (ReturnConfirmer) before it believes it. Empty: the provider has no such step, or ReturnURL is used.
	CallbackURL string
	// Buyer is the customer, for a provider that vets the payer. Nil for a guest nothing is known about; CustomerEmail and CustomerName are then all there is.
	Buyer *Buyer
}

// InvoiceCheckoutProvider is a provider whose hosted checkout can take a payment for an invoice.
type InvoiceCheckoutProvider interface {
	Provider
	CreateInvoiceCheckout(ctx context.Context, r CheckoutInvoiceRequest) (Checkout, error)
}

// InstructionLine is one labelled detail of how to pay (an IBAN, a bank name).
type InstructionLine struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Instructions tell a customer how to pay outside any checkout (a bank transfer). Reference is what the customer must put on the payment.
type Instructions struct {
	Reference string            `json:"reference"`
	Lines     []InstructionLine `json:"lines"`
}

// InstructionsRequest asks a manual provider how the customer pays an invoice.
type InstructionsRequest struct {
	IdempotencyKey string
	InvoiceID      string
	Number         string
	AmountMinor    int64
	Currency       string
}

// InstructionsProvider is a provider the customer pays by hand, away from any checkout (a bank-transfer provider). A tenant's account at such a provider
// shows up as a payment method on hosted pages without any change to them.
type InstructionsProvider interface {
	Provider
	PaymentInstructions(ctx context.Context, r InstructionsRequest) (Instructions, error)
}
