package payments

import (
	"context"
	"time"
)

// The optional interfaces below are what a provider can do beyond charging. A provider implements the ones its API has; Quotify's operator
// API offers an operation only when the provider's adapter implements the interface (otherwise ErrUnsupported). They are provider-neutral:
// the Paddle adapter maps them onto Paddle's products, prices, customers, subscriptions and discounts, and another provider could map them on its own.

// PriceSpec is one recurring price of a plan version.
type PriceSpec struct {
	// Key identifies the price in Quotify, e.g. "{plan version id}|year|EUR". The provider stores it with the price.
	Key         string
	Currency    string
	AmountMinor int64
	// Interval is "month" or "year".
	Interval string
	// Name is shown to the customer, e.g. "Pro (yearly)".
	Name string
}

// CatalogueRequest asks a provider to hold a plan's product and the given prices. The caller sends only prices that are not there yet.
type CatalogueRequest struct {
	// ProductRef is the provider's product for the plan, or empty to create it.
	ProductRef      string
	ProductName     string
	Description     string
	TaxCategory     string
	QuotifyPlanCode string
	Prices          []PriceSpec
}

// CatalogueResult is what the provider now holds.
type CatalogueResult struct {
	ProductRef string
	// Prices maps PriceSpec.Key to the provider's price id.
	Prices map[string]string
	// Skipped maps the key of a price that was not created to why (for example a currency the provider cannot charge).
	Skipped map[string]string
}

// Catalogue is a provider that holds products and prices for what Quotify sells.
type Catalogue interface {
	Provider
	SyncCatalogue(ctx context.Context, r CatalogueRequest) (CatalogueResult, error)
}

// CustomerSpec is a customer as a provider needs to know them.
type CustomerSpec struct {
	// QuotifyCustomerID ties the provider's customer to ours.
	QuotifyCustomerID string
	Email             string
	Name              string
	Country           string
	PostalCode        string
	City              string
	Address           string
	// TaxID and Business make the customer a business with a tax identifier.
	TaxID    string
	Business string
}

// CustomerRefs are the provider's ids of a customer.
type CustomerRefs struct {
	CustomerRef string
	AddressRef  string
	BusinessRef string
}

// CustomerAPI is a provider that holds customers.
type CustomerAPI interface {
	Provider
	// EnsureCustomer finds the customer by e-mail or creates it, and makes sure it has the address and business the spec names.
	EnsureCustomer(ctx context.Context, c CustomerSpec) (CustomerRefs, error)
	// PortalLink returns a page where the customer manages payment methods and sees invoices.
	PortalLink(ctx context.Context, customerRef string, subscriptionRefs []string) (string, error)
}

// ChangeItem is one thing a subscription is billed for after a change.
type ChangeItem struct {
	PriceRef string
	Quantity int
}

// ProviderChange moves a subscription the provider runs to other prices.
type ProviderChange struct {
	Items []ChangeItem
	// Timing is "immediately" or "period_end"; Proration is "prorate", "full" or "none" (the meanings of subscription.Timing and subscription.Proration).
	Timing    string
	Proration string
}

// ChangePreview is what a provider says a change would cost.
type ChangePreview struct {
	Currency string
	// CreditMinor and ChargeMinor are the provider's prorated credit and charge; ResultMinor is what is billed now (positive) or credited (negative).
	CreditMinor, ChargeMinor, ResultMinor int64
	// NextBilledAt and NextAmountMinor describe the next renewal after the change.
	NextBilledAt    *time.Time
	NextAmountMinor int64
}

// SubscriptionAPI is a provider that lets Quotify drive a subscription the provider runs.
type SubscriptionAPI interface {
	Provider
	PreviewChange(ctx context.Context, ref string, c ProviderChange) (ChangePreview, error)
	ApplyChange(ctx context.Context, ref string, c ProviderChange) error
	// Pause pauses at once or at the end of the paid period, optionally until resumeAt.
	Pause(ctx context.Context, ref string, atPeriodEnd bool, resumeAt *time.Time) error
	Resume(ctx context.Context, ref string) error
	// PaymentMethodLink returns a page where the customer updates the payment method of the subscription.
	PaymentMethodLink(ctx context.Context, ref string) (string, error)
}

// DiscountSpec is a coupon at a provider.
type DiscountSpec struct {
	Description string
	// Type is "percentage" or "flat". Amount is a percentage ("10" for 10 %) or minor units of Currency.
	Type     string
	Amount   string
	Currency string
	Code     string
	// Recurring discounts apply to every period, or to MaxIntervals periods when it is set.
	Recurring    bool
	MaxIntervals int
	UsageLimit   int
	ExpiresAt    *time.Time
}

// DiscountRefs are the provider's ids of a discount.
type DiscountRefs struct {
	DiscountRef string
	Code        string
}

// DiscountAPI is a provider that holds discounts.
type DiscountAPI interface {
	Provider
	CreateDiscount(ctx context.Context, d DiscountSpec) (DiscountRefs, error)
}

// DocumentLinker is a provider that has a document for a payment (the invoice or receipt it issued).
type DocumentLinker interface {
	Provider
	InvoiceURL(ctx context.Context, transactionRef string) (string, error)
}
