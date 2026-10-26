// Package einvoice turns a Payrudder invoice or credit note into a legal e-document and hands it to an integrator.
//
// The model here is provider-neutral and free of protobuf: the worker maps the invoice snapshot of a command to a Document, the UBL builder
// (package ubl) renders it, and a Provider delivers it. Money is minor units throughout; rates are basis points (2000 is 20 %).
package einvoice

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Kind of source document.
type Kind string

const (
	// KindInvoice is an invoice.
	KindInvoice Kind = "invoice"
	// KindCreditNote is a credit note (iade in Turkey).
	KindCreditNote Kind = "credit_note"
)

// DocumentType is the legal channel a document travels: it decides the UBL customisation and the integrator endpoint.
type DocumentType string

const (
	// TypeEFatura is Turkey's e-fatura, between two registered businesses (UBL-TR 1.2, TEMELFATURA).
	TypeEFatura DocumentType = "e-fatura"
	// TypeEArsiv is Turkey's e-arşiv, for buyers who are not registered (UBL-TR 1.2, EARSIVFATURA).
	TypeEArsiv DocumentType = "e-arsiv"
	// TypePeppol is EN 16931 / Peppol BIS Billing 3.0, the EU's channel.
	TypePeppol DocumentType = "peppol"
)

// Valid reports whether t is a known document type.
func (t DocumentType) Valid() bool { return t == TypeEFatura || t == TypeEArsiv || t == TypePeppol }

// Party is a seller or a buyer. Country is ISO 3166-1 alpha-2.
type Party struct {
	Name string
	// TaxID is the VKN (10 digits) or TCKN (11 digits) in Turkey, the VAT id (with country prefix) in the EU.
	TaxID string
	// TaxOffice is Turkey's vergi dairesi; empty elsewhere.
	TaxOffice string
	// RegistrationNo is the MERSIS number (Turkey) or the company registration number.
	RegistrationNo string
	// TradeRegistryNo is Turkey's ticaret sicil number.
	TradeRegistryNo string
	Street          string
	AdditionalLine  string
	City            string
	Region          string
	PostalCode      string
	Country         string
	Email           string
	// EInvoiceRegistered: the party is registered for e-fatura (a buyer's flag).
	EInvoiceRegistered bool
	// Alias is the party's e-fatura mailbox label (urn:mail:…) when the integrator needs one.
	Alias string
}

// LineKind says how a line adds to the document.
type LineKind string

const (
	// LineItem is something sold. Its amount is positive and exclusive of tax.
	LineItem LineKind = "item"
	// LineDiscount lowers the taxable base; its amount is stored positive (the size of the discount).
	LineDiscount LineKind = "discount"
	// LineTax is a tax line of the source invoice; the builder reads its rate and amount, it is not an item.
	LineTax LineKind = "tax"
	// LineCredit is a credit balance applied to the invoice: it pays part of the total (PrepaidAmount) and changes no tax. Stored positive.
	LineCredit LineKind = "credit"
)

// Line is a line of the source invoice or credit note.
type Line struct {
	No          int
	Kind        LineKind
	Description string
	// Quantity is an exact decimal ("1.250") or empty for a flat amount; UnitLabel names the unit ("GB").
	Quantity  string
	UnitLabel string
	// AmountMinor is positive for every kind (see LineKind).
	AmountMinor int64
	// ServiceFrom and ServiceTo are the invoiced service period when known.
	ServiceFrom, ServiceTo time.Time
}

// Reference is the document a credit note corrects.
type Reference struct {
	// Number is the legal number the original was issued under (Payrudder's number when it has none).
	Number    string
	IssueDate time.Time
}

// Document is everything a builder and a provider need.
type Document struct {
	Kind Kind
	Type DocumentType
	// UUID is the document's identity at the integrator and the UBL cbc:UUID; it is the idempotency key of the issue.
	UUID string
	// Number is Payrudder's own number (INV-2026-000123 / CN-2026-000007). The legal number is assigned at issue.
	Number    string
	IssueDate time.Time
	// Currency is ISO 4217; Exponent its minor-unit exponent.
	Currency string
	Exponent int
	Seller   Party
	Buyer    Party
	Lines    []Line
	// Reference is set on credit notes.
	Reference *Reference
	// Reason is the credit note's reason, printed as a note.
	Reason string
	// Sandbox marks a document of a sandbox tenant: builders say so in a note and providers other than the mock refuse it.
	Sandbox bool
}

// State of a document at the integrator.
type State string

const (
	// StatePending: accepted by the integrator, not yet confirmed by the authority.
	StatePending State = "pending"
	// StateIssued: the authority (or the integrator on its behalf) accepted it; it has a legal number.
	StateIssued State = "issued"
	// StateRejected: refused for good.
	StateRejected State = "rejected"
	// StateCancelled: cancelled where the law allows it.
	StateCancelled State = "cancelled"
)

// IssueRequest asks a provider to issue a document.
type IssueRequest struct {
	Doc Document
	// UBL is the document rendered by the builder for Doc.Type. Providers that take a structured model instead (a JSON API) may ignore it.
	UBL []byte
}

// Ref identifies an issued document at the integrator.
type Ref struct {
	ExternalID string
	// UUID is the document UUID Payrudder sent.
	UUID string
	Type DocumentType
}

// IssueResult is the integrator's answer to an issue.
type IssueResult struct {
	Ref
	LegalNumber string
	State       State
	// PDF is the integrator's rendering when it returned one with the answer; otherwise fetched with FetchPDF.
	PDF []byte
}

// StatusResult is the current state of a document.
type StatusResult struct {
	State       State
	LegalNumber string
	// Detail is the integrator's own status text, for the operator.
	Detail string
}

// Capabilities says what an adapter does.
type Capabilities struct {
	// Name is the registry name ("mock", "nilvera", "webhook").
	Name string
	// Countries the adapter can issue for (ISO alpha-2). Empty means any.
	Countries []string
	// Types are the document types it can send.
	Types []DocumentType
	// NeedsBuyerCheck: whether the buyer is an e-fatura user must be asked of the integrator (Party.EInvoiceRegistered is unknown to Payrudder).
	NeedsBuyerCheck bool
	// CanCancel: the adapter can cancel an issued document (where the type allows).
	CanCancel bool
	// ReturnsPDF: the integrator renders a legal PDF; otherwise Payrudder's own invoice PDF is the only copy.
	ReturnsPDF bool
}

// Supports reports whether the adapter sends t.
func (c Capabilities) Supports(t DocumentType) bool {
	for _, x := range c.Types {
		if x == t {
			return true
		}
	}
	return false
}

// Provider is an e-invoice integrator.
type Provider interface {
	Capabilities() Capabilities
	// Issue sends the document. It must be idempotent on Doc.UUID: a repeat returns the existing document's result (StatusResult semantics), never a second one.
	Issue(ctx context.Context, r IssueRequest) (IssueResult, error)
	// Status fetches the current state.
	Status(ctx context.Context, ref Ref) (StatusResult, error)
	// Cancel cancels an issued document where legal; ErrUnsupported otherwise.
	Cancel(ctx context.Context, ref Ref, reason string) error
	// FetchPDF returns the integrator's PDF of the document.
	FetchPDF(ctx context.Context, ref Ref) ([]byte, error)
	// FetchUBL returns the UBL the authority holds (sealed by the integrator).
	FetchUBL(ctx context.Context, ref Ref) ([]byte, error)
}

// BuyerChecker is implemented by providers that can tell whether a tax id is registered for e-fatura.
type BuyerChecker interface {
	// IsEInvoiceUser reports registration and the mailbox alias to use.
	IsEInvoiceUser(ctx context.Context, taxID string) (registered bool, alias string, err error)
}

// Errors an adapter returns. Anything else is treated as retryable.
var (
	// ErrUnsupported: the adapter cannot do this (cancel on e-fatura, a PDF the integrator does not render).
	ErrUnsupported = errors.New("einvoice: operation not supported by the provider")
	// ErrNotFound: the integrator does not know the document.
	ErrNotFound = errors.New("einvoice: document not found at the provider")
)

// PermanentError is a refusal that a retry will not change: validation failed, the credentials are wrong, the tax id is unknown.
type PermanentError struct {
	// Code is the integrator's code (or ours) for the refusal; Message a one-line explanation without credentials.
	Code, Message string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("einvoice: refused (%s): %s", e.Code, e.Message)
}

// TransientError is a failure worth retrying: a timeout, a 5xx, a 429.
type TransientError struct{ Err error }

func (e *TransientError) Error() string {
	return "einvoice: transient provider error: " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *TransientError) Unwrap() error { return e.Err }
