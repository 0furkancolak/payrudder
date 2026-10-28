package model

import (
	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
)

// Snapshot is what the saga's commands need about the customer and the tenant, frozen when the draft was created.
type Snapshot struct {
	CustomerExternalID string `json:"customer_external_id"`
	CustomerName       string `json:"customer_name"`
	CustomerEmail      string `json:"customer_email"`
	// CustomerLocale is the language of the customer (en, tr, de, fr, es, ar) and CustomerType "individual" or "business", as the customer had them when the draft was made; empty for none.
	CustomerLocale string `json:"customer_locale,omitempty"`
	CustomerType   string `json:"customer_type,omitempty"`
	PaymentMethod  string `json:"payment_method"`
	// Provider names the payment provider of the customer's subscription; empty is the tenant's default.
	Provider         string `json:"provider,omitempty"`
	TenantName       string `json:"tenant_name"`
	Environment      string `json:"environment"` // "sandbox" | "live"
	CurrencyExponent int    `json:"currency_exponent"`
	LogoObjectKey    string `json:"logo_object_key,omitempty"`
	// ProviderMails: the provider takes the payment and mails the customer its own receipt (decided when the draft was made), so Payrudder sends none.
	ProviderMails bool `json:"provider_mails,omitempty"`
	// The postal addresses as printed lines, frozen when the draft was made.
	SellerAddress   []string `json:"seller_address,omitempty"`
	CustomerAddress []string `json:"customer_address,omitempty"`
	// E-invoicing (phase 30): the tenant's e_invoice_on and the parties, frozen with the draft. Empty when the tenant does not use it.
	EInvoiceOn string `json:"e_invoice_on,omitempty"`
	Seller     *Party `json:"seller,omitempty"`
	Buyer      *Party `json:"buyer,omitempty"`
	// Collection is "link" for an invoice the customer pays through a payment link: the saga waits for the payment instead of charging a stored method.
	Collection string `json:"collection,omitempty"`
	// SkipMail: no mail goes to the customer (a guest paying an ad-hoc link has no address on file).
	SkipMail bool `json:"skip_mail,omitempty"`
	// Description is what an ad-hoc link is for, shown on the payment page.
	Description string `json:"description,omitempty"`
	// Country of the customer (ISO 3166-1 alpha-2), for the payment methods routing picks.
	Country string `json:"country,omitempty"`
	// BankTransfer are the tenant's bank details, for an invoice whose provider is a bank transfer; frozen when the draft was made. The payment reference
	// is worked out from the template and the invoice number.
	BankTransfer *BankTransfer `json:"bank_transfer,omitempty"`
}

// CollectionLink is the Snapshot.Collection of an invoice the customer pays through a payment link.
const CollectionLink = "link"

// BankTransfer is where an invoice is paid by bank transfer.
type BankTransfer struct {
	BankName          string `json:"bank_name"`
	AccountHolder     string `json:"account_holder"`
	IBAN              string `json:"iban"`
	BIC               string `json:"bic,omitempty"`
	ReferenceTemplate string `json:"reference_template,omitempty"`
}

// Instructions are the bank details with the payment reference of an invoice, or nil when the invoice is not paid by bank transfer.
func (s Snapshot) Instructions(number string, invoiceID uuid.UUID) *BankInstructions {
	b := s.BankTransfer
	if b == nil {
		return nil
	}
	return &BankInstructions{BankName: b.BankName, AccountHolder: b.AccountHolder, IBAN: b.IBAN, BIC: b.BIC,
		Reference: banktransfer.Reference(b.ReferenceTemplate, number, invoiceID.String())}
}

// BankInstructions are the bank details and the reference the customer writes on the transfer.
type BankInstructions struct{ BankName, AccountHolder, IBAN, BIC, Reference string }

// Party is a seller or buyer as frozen into an invoice snapshot, with the legal data an e-invoice needs.
type Party struct {
	Name               string `json:"name,omitempty"`
	TaxID              string `json:"tax_id,omitempty"`
	TaxOffice          string `json:"tax_office,omitempty"`
	RegistrationNo     string `json:"registration_no,omitempty"`
	TradeRegistryNo    string `json:"trade_registry_no,omitempty"`
	Line1              string `json:"line1,omitempty"`
	Line2              string `json:"line2,omitempty"`
	City               string `json:"city,omitempty"`
	Region             string `json:"region,omitempty"`
	PostalCode         string `json:"postal_code,omitempty"`
	Country            string `json:"country,omitempty"`
	Email              string `json:"email,omitempty"`
	EInvoiceRegistered bool   `json:"e_invoice_registered,omitempty"`
}

// Proto is the party as a command carries it.
func (p *Party) Proto() *commandsv1.PartySnapshot {
	if p == nil {
		return nil
	}
	return &commandsv1.PartySnapshot{Name: p.Name, TaxId: p.TaxID, TaxOffice: p.TaxOffice, RegistrationNo: p.RegistrationNo, TradeRegistryNo: p.TradeRegistryNo,
		Line1: p.Line1, Line2: p.Line2, City: p.City, Region: p.Region, PostalCode: p.PostalCode, Country: p.Country, Email: p.Email, EInvoiceRegistered: p.EInvoiceRegistered}
}

// PartyOf builds a party from a tenant's or customer's address and the legal data given.
func PartyOf(name string, a *plansv1.Address, taxID, taxOffice, registrationNo, tradeRegistryNo, email string, registered bool) *Party {
	return &Party{Name: name, TaxID: taxID, TaxOffice: taxOffice, RegistrationNo: registrationNo, TradeRegistryNo: tradeRegistryNo,
		Line1: a.GetLine1(), Line2: a.GetLine2(), City: a.GetCity(), Region: a.GetRegion(), PostalCode: a.GetPostalCode(), Country: a.GetCountry(),
		Email: email, EInvoiceRegistered: registered}
}
