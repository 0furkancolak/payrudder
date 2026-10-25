// Package banktransfer is the bank transfer (havale/EFT/SEPA) provider of payments.Provider.
//
// Nothing is ever sent to a bank. A charge tells the customer where to send the money: it returns a *payments.PendingError that carries the payment
// reference (by default the invoice number), and the operator confirms what arrived through payments-worker's receipt endpoint, which writes the
// ledger. Payrudder owns renewal and dunning; there is no hosted checkout and no refund API (Refunds=false: money goes back to the customer outside Payrudder).
// Because a charge has no side effect it needs no idempotency of its own, but IdempotentCharges is false so that the status query reads the ledger.
package banktransfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Name is the registry name.
const Name = "banktransfer"

// MaxPaymentWindowDays is the longest payment window a tenant can set.
const MaxPaymentWindowDays = 90

// Settings is the JSON a tenant's bank transfer account is stored as. It holds no secret; the account is stored sealed like the others, and the public
// part is printed on the tenant's invoices.
type Settings struct {
	BankName      string `json:"bank_name"`
	AccountHolder string `json:"account_holder"`
	IBAN          string `json:"iban"`
	// BIC (SWIFT) is optional.
	BIC string `json:"bic,omitempty"`
	// ReferenceTemplate is what the customer writes on the transfer: {invoice_number} and {invoice_id} are filled in. Empty is the invoice number.
	ReferenceTemplate string `json:"reference_template,omitempty"`
	// Currencies the account takes. Empty means any.
	Currencies []string `json:"currencies,omitempty"`
	// PaymentWindowDays is how long an invoice waits for its transfer before the payment counts as failed and dunning applies. Zero is the deployment's
	// default (PAYMENTS_TRANSFER_WINDOW_DAYS).
	PaymentWindowDays int `json:"payment_window_days,omitempty"`
}

var (
	bicRe      = regexp.MustCompile(`^[A-Z]{4}[A-Z]{2}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
	currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Normalize validates the settings and returns them in their stored form: the IBAN and BIC without spaces and in capitals, the currency list upper-case
// and without duplicates, the texts trimmed.
func (s Settings) Normalize() (Settings, error) {
	var errs []error
	s.BankName, s.AccountHolder = strings.TrimSpace(s.BankName), strings.TrimSpace(s.AccountHolder)
	for field, v := range map[string]string{"bank_name": s.BankName, "account_holder": s.AccountHolder} {
		switch {
		case v == "":
			errs = append(errs, fmt.Errorf("%s is required", field))
		case utf8.RuneCountInString(v) > 100 || strings.ContainsAny(v, "\n\r\t\x00"):
			errs = append(errs, fmt.Errorf("%s must be one line of at most 100 characters", field))
		}
	}
	iban, err := ValidateIBAN(s.IBAN)
	if err != nil {
		errs = append(errs, fmt.Errorf("iban: %w", err))
	}
	s.IBAN = iban
	if s.BIC = strings.ToUpper(strings.ReplaceAll(s.BIC, " ", "")); s.BIC != "" && !bicRe.MatchString(s.BIC) {
		errs = append(errs, errors.New("bic must be 8 or 11 characters, like TCZBTR2A or TCZBTR2AXXX"))
	}
	if s.ReferenceTemplate = strings.TrimSpace(s.ReferenceTemplate); s.ReferenceTemplate != "" {
		if err := ValidateTemplate(s.ReferenceTemplate); err != nil {
			errs = append(errs, err)
		}
	}
	var currencies []string
	for _, c := range s.Currencies {
		c = strings.ToUpper(strings.TrimSpace(c))
		if !currencyRe.MatchString(c) {
			errs = append(errs, fmt.Errorf("currencies: %q is not an ISO 4217 code", c))
			continue
		}
		if !slices.Contains(currencies, c) {
			currencies = append(currencies, c)
		}
	}
	s.Currencies = currencies
	if s.PaymentWindowDays < 0 || s.PaymentWindowDays > MaxPaymentWindowDays {
		errs = append(errs, fmt.Errorf("payment_window_days must be 0 (the default) to %d", MaxPaymentWindowDays))
	}
	return s, errors.Join(errs...)
}

// Parse reads and validates the stored JSON.
func Parse(settings []byte) (Settings, error) {
	var s Settings
	if err := json.Unmarshal(settings, &s); err != nil {
		return Settings{}, fmt.Errorf("banktransfer: settings: %w", err)
	}
	s, err := s.Normalize()
	if err != nil {
		return Settings{}, fmt.Errorf("banktransfer: %w", err)
	}
	return s, nil
}

// Instructions are what the customer needs to pay: where, and what to write on the transfer.
type Instructions struct {
	BankName      string `json:"bank_name"`
	AccountHolder string `json:"account_holder"`
	IBAN          string `json:"iban"`
	BIC           string `json:"bic,omitempty"`
	Reference     string `json:"reference"`
}

// Provider is a tenant's bank transfer account.
type Provider struct{ s Settings }

// New returns a provider for validated settings.
func New(s Settings) (*Provider, error) {
	s, err := s.Normalize()
	if err != nil {
		return nil, fmt.Errorf("banktransfer: %w", err)
	}
	return &Provider{s: s}, nil
}

// Factory builds a Provider from stored settings, for payments.Registry.
func Factory(settings []byte) (payments.Provider, error) {
	s, err := Parse(settings)
	if err != nil {
		return nil, err
	}
	return &Provider{s: s}, nil
}

// Register adds the provider to a registry. It has no webhook: nothing notifies Payrudder of a transfer, the operator confirms it.
func Register(r *payments.Registry) { r.Register(Name, Factory) }

// Name implements payments.PaymentProvider.
func (p *Provider) Name() string { return Name }

// Settings returns the account's settings in their stored form.
func (p *Provider) Settings() Settings { return p.s }

// Caps are the capabilities that do not depend on an account: routing rules are checked against them before any account exists.
func Caps() payments.Capabilities {
	return payments.Capabilities{Name: Name}
}

// Capabilities implements payments.Provider: Payrudder owns renewal and dunning, there is no hosted checkout and no refund API, and the status query reads
// the ledger (IdempotentCharges=false).
func (p *Provider) Capabilities() payments.Capabilities {
	c := Caps()
	c.Currencies = slices.Clone(p.s.Currencies)
	return c
}

// PaymentWindow implements payments.ManualCollector.
func (p *Provider) PaymentWindow() time.Duration {
	return time.Duration(p.s.PaymentWindowDays) * 24 * time.Hour
}

// Instructions returns the bank details and the reference for an invoice.
func (p *Provider) Instructions(invoiceNumber, invoiceID string) Instructions {
	return Instructions{BankName: p.s.BankName, AccountHolder: p.s.AccountHolder, IBAN: p.s.IBAN, BIC: p.s.BIC,
		Reference: Reference(p.s.ReferenceTemplate, invoiceNumber, invoiceID)}
}

// Charge implements payments.PaymentProvider. It calls nothing: it validates the request and returns a *payments.PendingError carrying the payment reference.
func (p *Provider) Charge(_ context.Context, r payments.ChargeRequest) (payments.ChargeResult, error) {
	if r.IdempotencyKey == "" {
		return payments.ChargeResult{}, errors.New("banktransfer: idempotency key is mandatory")
	}
	if r.AmountMinor <= 0 {
		return payments.ChargeResult{}, errors.New("banktransfer: the amount must be positive")
	}
	if !p.Capabilities().CanCharge(r.Currency) {
		return payments.ChargeResult{}, fmt.Errorf("banktransfer: the account does not take %s", r.Currency)
	}
	return payments.ChargeResult{}, &payments.PendingError{Ref: p.Instructions(r.InvoiceNumber, r.InvoiceID).Reference}
}

// Refund implements payments.PaymentProvider: there is no refund API; the operator pays the customer back and records it outside Payrudder.
func (p *Provider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	return payments.RefundResult{}, payments.ErrUnsupported
}

// Status implements payments.PaymentProvider. The provider knows nothing; the outcome is in the ledger, which the handler reads. A caller that gets here
// has no ledger fact, so the payment is still pending.
func (p *Provider) Status(context.Context, string) (payments.StatusResult, error) {
	return payments.StatusResult{}, &payments.PendingError{Ref: "awaiting the bank transfer"}
}

var _ payments.ManualCollector = (*Provider)(nil)
