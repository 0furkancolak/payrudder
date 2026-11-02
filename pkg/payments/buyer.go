package payments

import (
	"context"
	"net/url"
	"strings"
)

// Buyer is the person who pays, for a provider that vets the payer and so needs to know them (iyzico's buyer, shipping and billing address). payments-worker builds it
// from the customer record. A field the record does not have stays empty: the adapter names the ones its provider cannot do without (BuyerIncompleteError) and never
// makes one up. IP is the payer's address when a person is at the other end of the request (a hosted page); a renewal Payrudder charges on its own has none.
type Buyer struct {
	// ID is the customer's id in Payrudder.
	ID string
	// Name is the full name as the customer record holds it.
	Name  string
	Email string
	// Phone is E.164 ("+905321234567").
	Phone string
	// TaxID is the customer's tax or national identity number (TCKN, VKN, VAT id).
	TaxID string
	Line1 string
	Line2 string
	City  string
	// Region is the state or province.
	Region     string
	PostalCode string
	// Country is the ISO 3166-1 alpha-2 code.
	Country string
	IP      string
}

// BuyerIncompleteError: the provider cannot take the payment without these fields of the buyer, or refused one it was given, and neither the customer record nor the
// account's explicit defaults have a usable value. Nothing was charged. Missing names the fields as the customer record does ("phone", "tax_id", "address.line1",
// "address.city", "country", "name", "surname", "email"). Retrying changes nothing until someone completes the customer, so it is neither a decline of the card nor
// a failure to retry blindly.
type BuyerIncompleteError struct {
	Provider string
	Missing  []string
}

func (e *BuyerIncompleteError) Error() string {
	return e.Provider + " needs more about the buyer: " + strings.Join(e.Missing, ", ")
}

// ReturnRequest is what comes back to Payrudder when a payer returns from a page of the provider's (the checkout form, the bank's 3-D Secure page): the form the
// provider posted to the callback URL. The browser of the payer carried it, so it names the payment but proves nothing.
type ReturnRequest struct {
	// IdempotencyKey, InvoiceID, AmountMinor and Currency are those of the attempt the payer was sent away from, read from payments-worker's own records.
	IdempotencyKey string
	InvoiceID      string
	AmountMinor    int64
	Currency       string
	// ProviderRef is the provider's id the attempt was started with (the checkout token, the 3-D Secure payment id), when payments-worker has it. A form that names
	// another one does not belong to the attempt.
	ProviderRef string
	Form        url.Values
	// PaidMinor is what the payer was quoted to pay in all when the attempt was split into instalments (the price plus the bank's charge for them); 0 for a payment in
	// one go. The invoice is paid by AmountMinor either way.
	PaidMinor int64
}

// ReturnConfirmer is a provider that sends the payer back to a callback URL of ours with a form (iyzico). ConfirmReturn asks the provider what the payment is and
// answers a StatusResult: StateSucceeded or StateDeclined when the provider says so, *PendingError when it holds the payment for review, and any other error when the
// outcome is not known (it is never turned into a decline). Whatever the form claims is not believed, and a form that proves to belong to another payment than the
// attempt is payments.ErrUnverified.
type ReturnConfirmer interface {
	Provider
	ConfirmReturn(ctx context.Context, r ReturnRequest) (StatusResult, error)
}

// ThreeDSRequest asks a provider to authenticate a charge of a stored payment method with the payer at their bank (3-D Secure).
type ThreeDSRequest struct {
	IdempotencyKey string
	InvoiceID      string
	Number         string
	AmountMinor    int64
	Currency       string
	// PaymentMethod is the customer's stored card in the form the provider wants.
	PaymentMethod string
	// CallbackURL is where the provider posts the result of the authentication, through the payer's browser.
	CallbackURL string
	Buyer       *Buyer
	// Installments is the number of instalments the payer chose (0 or 1: one payment) and PaidMinor what the provider quoted for that many in all, charges included
	// (0: AmountMinor). The provider's own quote is the only figure that may be passed: the caller takes it from InstallmentOffer, never from the browser.
	Installments int
	PaidMinor    int64
}

// ThreeDSStart is the page the payer must be shown to authenticate.
type ThreeDSStart struct {
	// HTML is the page, ready to serve: it is an auto-submitting form that sends the payer to their bank.
	HTML string
	// Ref is the provider's id of the payment that now waits for the authentication.
	Ref string
}

// ThreeDSProvider is a provider that authenticates a stored-card charge with the payer. The payment is completed by ConfirmReturn when the payer comes back.
type ThreeDSProvider interface {
	Provider
	// StoresPaymentMethod reports whether a customer's payment method token is a stored card of this provider (the form StartThreeDS wants), so that a page offers the
	// payment only to a customer who has one.
	StoresPaymentMethod(paymentMethod string) bool
	StartThreeDS(ctx context.Context, r ThreeDSRequest) (ThreeDSStart, error)
}
