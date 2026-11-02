package payments

import "context"

// StoredCard is a card a provider holds for a customer (never the number).
type StoredCard struct {
	Token       string
	Alias       string
	BIN         string
	Last4       string
	Type        string
	Association string
	Family      string
	Bank        string
	ExpireMonth string
	ExpireYear  string
}

// CardStore is a provider that keeps cards for its merchants' customers and lets the merchant list and delete them. The card is put there by whoever holds the
// number, not by Payrudder: no adapter takes a card number.
type CardStore interface {
	Provider
	// ListCards returns the cards the provider holds under the customer's user key.
	ListCards(ctx context.Context, userKey string) ([]StoredCard, error)
	DeleteCard(ctx context.Context, userKey, token string) error
}

// InstallmentOption is one way to split a payment.
type InstallmentOption struct {
	Count            int
	InstallmentMinor int64
	// TotalMinor is what the payer pays in all; more than the price when the card's bank charges for the instalments.
	TotalMinor int64
}

// InstallmentCard is a card family and what instalments it offers for an amount.
type InstallmentCard struct {
	BIN         string
	Type        string
	Association string
	Family      string
	Bank        string
	// Force3DS: the payment on this card must be authenticated with the payer.
	Force3DS bool
	Options  []InstallmentOption
}

// InstallmentQuoter is a provider that can say which instalments a card allows for an amount.
type InstallmentQuoter interface {
	Provider
	// Installments asks for the options for an amount. bin (the first 8 digits of a card) narrows the answer to that card; empty asks for every card family.
	Installments(ctx context.Context, amountMinor int64, currency, bin string) ([]InstallmentCard, error)
}
