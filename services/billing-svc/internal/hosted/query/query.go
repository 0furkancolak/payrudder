// Package query reads what a visitor with a payment link or a customer portal link may see. It never writes.
//
// A token resolves to a Scope (a tenant, and either one link or one customer). Everything after that is read inside that tenant's RLS transaction, and a portal
// visitor's invoice must belong to the portal's customer: a token never reaches another tenant's or another customer's data.
package query

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	invoices "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// errNoLink is what a visitor gets for a token that is unknown, expired or revoked, and for an invoice that is not in its scope: they all look the same.
var errNoLink = errkey.New(codes.NotFound, "billing.invoice_link.not_found", "this link does not exist or has expired")

// NoLink is the error of an unknown link, for callers that decide a link cannot be used.
func NoLink() error { return errNoLink }

// TokenHash is what is stored of a link token: the token itself is only ever shown once.
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Link is a payment link a token resolved to.
type Link struct {
	Row sqlc.FindActiveInvoiceLinkRow
}

// ResolveLink resolves a token to its link, or a not-found error.
func ResolveLink(ctx context.Context, system *pgxpool.Pool, token string) (*Link, error) {
	if len(token) < 20 || len(token) > 100 {
		return nil, errNoLink
	}
	row, err := sqlc.New(system).FindActiveInvoiceLink(ctx, TokenHash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoLink
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &Link{Row: row}, nil
}

// Scope is what a visitor's token grants.
type Scope struct {
	TenantID uuid.UUID
	// Link is set for a payment link, Portal for a customer portal.
	Link   *Link
	Portal *Portal
	// InvoiceID is the invoice in scope; nil for an ad-hoc link before its first use and for the portal's list.
	InvoiceID uuid.UUID
	ExpiresAt time.Time
}

// Portal is a customer portal link a token resolved to.
type Portal struct {
	CustomerID uuid.UUID
}

// Resolve turns the credentials of a request into a scope: a link token, or a portal token with the invoice the visitor asks for (which must be the customer's).
func Resolve(ctx context.Context, system *pgxpool.Pool, token, portalToken, invoiceID string) (*Scope, error) {
	switch {
	case token != "" && portalToken != "":
		return nil, errNoLink
	case token != "":
		l, err := ResolveLink(ctx, system, token)
		if err != nil {
			return nil, err
		}
		s := &Scope{TenantID: l.Row.TenantID, Link: l, ExpiresAt: l.Row.ExpiresAt}
		if l.Row.InvoiceID != nil {
			s.InvoiceID = *l.Row.InvoiceID
		}
		return s, nil
	case portalToken != "":
		return resolvePortal(ctx, system, portalToken, invoiceID)
	}
	return nil, errNoLink
}

func resolvePortal(ctx context.Context, system *pgxpool.Pool, token, invoiceID string) (*Scope, error) {
	if len(token) < 20 || len(token) > 100 {
		return nil, errNoLink
	}
	row, err := sqlc.New(system).FindActivePortalLink(ctx, TokenHash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoLink
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	s := &Scope{TenantID: row.TenantID, Portal: &Portal{CustomerID: row.CustomerID}, ExpiresAt: row.ExpiresAt}
	if invoiceID == "" {
		return s, nil
	}
	id, err := uuid.Parse(invoiceID)
	if err != nil {
		return nil, errNoLink
	}
	// The invoice must be this customer's, in this tenant: the predicate names both, besides the RLS of the transaction.
	err = pgxx.InTx(ctx, system, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		_, err := sqlc.New(tx).GetInvoiceOfCustomer(ctx, sqlc.GetInvoiceOfCustomerParams{TenantID: row.TenantID, CustomerID: row.CustomerID, ID: id})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoLink
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	s.InvoiceID = id
	return s, nil
}

// State is the payment state a visitor sees, and whether the invoice can be paid now.
//
// The saga says what is happening: a link-collected invoice waits (AWAITING_PAYMENT), an invoice in dunning has had a declined charge (PAYMENT_DECLINED, payable
// too), a charge in flight is processing. Anything before the PDF is rendered is processing: the customer cannot pay what is not ready.
func State(invoiceStatus, sagaState string) (state string, payable bool) {
	switch invoiceStatus {
	case "paid":
		return "paid", false
	case "void":
		return "void", false
	case "uncollectible":
		return "failed", false
	}
	switch saga.State(sagaState) {
	case saga.AwaitingPayment:
		return "open", true
	case saga.PaymentDeclined:
		return "failed", true
	}
	return "processing", false
}

// Seller is the party that issues the invoices of a link: the tenant.
type Seller struct {
	Name        string
	LogoKey     string
	Environment string
}

// SellerLookup finds the seller of a tenant (an ad-hoc link before its first use has no invoice to read it from).
type SellerLookup func(ctx context.Context, tenantID uuid.UUID) (Seller, error)

// Deps are what the reads need besides the database.
type Deps struct {
	App    *pgxpool.Pool
	System *pgxpool.Pool
	PDFs   invoices.PDFSigner
	Seller SellerLookup
}

// Adhoc is an ad-hoc link as a visitor sees it before paying.
type Adhoc struct {
	Description      string
	AmountMinor      int64
	Currency         string
	CurrencyExponent int32
	Consumed         bool
}

// Page is what the gateway renders for a link or an invoice of a portal.
type Page struct {
	// Kind is invoice or adhoc.
	Kind    string
	Invoice *invoices.Invoice
	Adhoc   *Adhoc
	// Meta of the invoice.
	Snapshot             saga.Snapshot
	SagaState            string
	SagaID               uuid.UUID
	Provider             string
	Country              string
	CustomerID           uuid.UUID
	ExpiresAt            time.Time
	SellerName           string
	CustomerName         string
	CurrencyExponent     int32
	SellerAddressLines   []string
	CustomerAddressLines []string
	PDFAvailable         bool
	LogoAvailable        bool
	Environment          string
	State                string
	Payable              bool
	// PaymentInstructions say where to send the money, for an open invoice paid by bank transfer.
	PaymentInstructions *saga.BankInstructions
}

// Get returns what the visitor in scope sees. An ad-hoc link before its first use returns the Adhoc part and no invoice.
func Get(ctx context.Context, d Deps, s *Scope) (*Page, error) {
	if s.Link != nil && s.Link.Row.Kind == "adhoc" {
		return d.adhoc(ctx, s)
	}
	if s.InvoiceID == uuid.Nil {
		return nil, errNoLink
	}
	return d.invoice(ctx, s)
}

func (d Deps) adhoc(ctx context.Context, s *Scope) (*Page, error) {
	r := s.Link.Row
	if r.AmountMinor == nil || r.Currency == nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	exp := int32(2)
	if c, ok := money.LookupCurrency(*r.Currency); ok {
		exp = int32(c.Exponent) //nolint:gosec // 0..3
	}
	seller, err := d.Seller(ctx, s.TenantID)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "seller lookup unavailable")
	}
	p := &Page{Kind: "adhoc", ExpiresAt: s.ExpiresAt, SellerName: seller.Name, Environment: seller.Environment, LogoAvailable: seller.LogoKey != "" && d.PDFs != nil,
		Adhoc: &Adhoc{AmountMinor: *r.AmountMinor, Currency: *r.Currency, CurrencyExponent: exp, Consumed: r.SingleUse && r.PaidCount > 0}}
	if r.Description != nil {
		p.Adhoc.Description = *r.Description
	}
	return p, nil
}

func (d Deps) invoice(ctx context.Context, s *Scope) (*Page, error) {
	p := &Page{Kind: "invoice", ExpiresAt: s.ExpiresAt}
	err := pgxx.InTenantTx(ctx, d.App, s.TenantID, func(tx pgx.Tx) error {
		inv, err := invoices.Load(ctx, tx, s.InvoiceID)
		if err != nil {
			return err
		}
		p.Invoice = inv
		meta, err := sqlc.New(tx).GetInvoiceForLink(ctx, sqlc.GetInvoiceForLinkParams{TenantID: s.TenantID, ID: s.InvoiceID})
		if err != nil {
			return err
		}
		if s.Portal != nil && meta.CustomerID != s.Portal.CustomerID {
			return pgx.ErrNoRows // belt and braces: Resolve checked it already
		}
		if err := json.Unmarshal(meta.Snapshot, &p.Snapshot); err != nil {
			return err
		}
		p.SagaState, p.SagaID, p.Provider, p.Country, p.CustomerID = meta.SagaState, meta.SagaID, meta.Provider, meta.Country, meta.CustomerID
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoLink
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if p.Invoice.Row.Status == "draft" {
		return nil, errNoLink
	}
	snap := p.Snapshot
	p.SellerName, p.CustomerName, p.CurrencyExponent = snap.TenantName, snap.CustomerName, int32(snap.CurrencyExponent) //nolint:gosec // 0..3
	p.SellerAddressLines, p.CustomerAddressLines = snap.SellerAddress, snap.CustomerAddress
	p.Environment = snap.Environment
	if p.Country == "" {
		p.Country = snap.Country
	}
	p.PDFAvailable = p.Invoice.Row.PdfObjectKey != "" && d.PDFs != nil
	p.LogoAvailable = snap.LogoObjectKey != "" && d.PDFs != nil
	p.State, p.Payable = State(p.Invoice.Row.Status, p.SagaState)
	if p.Invoice.Row.Status == "open" {
		p.PaymentInstructions = snap.Instructions(p.Invoice.Row.Number, s.InvoiceID)
	}
	return p, nil
}

// PDFURL returns a presigned link to the PDF of the invoice in scope.
func PDFURL(ctx context.Context, d Deps, s *Scope) (string, time.Time, error) {
	if s.InvoiceID == uuid.Nil {
		return "", time.Time{}, errNoLink
	}
	if d.PDFs == nil {
		return "", time.Time{}, status.Error(codes.Unimplemented, "object storage is not configured")
	}
	var key *string
	err := pgxx.InTenantTx(ctx, d.App, s.TenantID, func(tx pgx.Tx) error {
		var err error
		key, err = sqlc.New(tx).GetInvoicePDFKey(ctx, s.InvoiceID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, errNoLink
	}
	if err != nil {
		return "", time.Time{}, status.Error(codes.Internal, "internal error")
	}
	if key == nil || *key == "" {
		return "", time.Time{}, errkey.New(codes.FailedPrecondition, "billing.invoice.pdf_not_ready", "the invoice PDF is not rendered yet")
	}
	return invoices.Sign(ctx, d.PDFs, *key)
}

// LogoURL returns a short-lived link to the seller's logo, or a not-found error when there is none.
func LogoURL(ctx context.Context, d Deps, s *Scope) (string, error) {
	if d.PDFs == nil {
		return "", errNoLink
	}
	key := ""
	if s.InvoiceID != uuid.Nil {
		p, err := d.invoice(ctx, s)
		if err != nil {
			return "", err
		}
		key = p.Snapshot.LogoObjectKey
	}
	if key == "" {
		seller, err := d.Seller(ctx, s.TenantID)
		if err != nil {
			return "", status.Error(codes.Unavailable, "seller lookup unavailable")
		}
		key = seller.LogoKey
	}
	if key == "" {
		return "", errNoLink
	}
	url, _, err := invoices.Sign(ctx, d.PDFs, key)
	return url, err
}

// PortalInvoice is one row of the customer's page.
type PortalInvoice struct {
	Row   sqlc.ListPortalInvoicesRow
	State string
	Pay   bool
}

// PortalPage is the customer's page.
type PortalPage struct {
	Scope         *Scope
	CustomerName  string
	SellerName    string
	LogoAvailable bool
	Environment   string
	Invoices      []PortalInvoice
}

// portalLimit is how many invoices the page lists, newest first.
const portalLimit = 100

// GetPortal returns the customer's page: their finalized invoices, newest first.
func GetPortal(ctx context.Context, d Deps, s *Scope) (*PortalPage, error) {
	if s.Portal == nil {
		return nil, errNoLink
	}
	out := &PortalPage{Scope: s}
	var rows []sqlc.ListPortalInvoicesRow
	var latest []byte
	err := pgxx.InTenantTx(ctx, d.App, s.TenantID, func(tx pgx.Tx) error {
		var err error
		q := sqlc.New(tx)
		if rows, err = q.ListPortalInvoices(ctx, sqlc.ListPortalInvoicesParams{TenantID: s.TenantID, CustomerID: s.Portal.CustomerID, RowLimit: portalLimit}); err != nil {
			return err
		}
		if len(rows) > 0 {
			meta, err := q.GetInvoiceForLink(ctx, sqlc.GetInvoiceForLinkParams{TenantID: s.TenantID, ID: rows[0].ID})
			if err != nil {
				return err
			}
			latest = meta.Snapshot
		}
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	for _, r := range rows {
		st, pay := State(r.Status, r.SagaState)
		out.Invoices = append(out.Invoices, PortalInvoice{Row: r, State: st, Pay: pay})
	}
	var snap saga.Snapshot
	if latest != nil {
		_ = json.Unmarshal(latest, &snap)
	}
	out.CustomerName, out.SellerName, out.Environment = snap.CustomerName, snap.TenantName, snap.Environment
	logoKey := snap.LogoObjectKey
	if out.SellerName == "" || out.Environment == "" {
		seller, err := d.Seller(ctx, s.TenantID)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "seller lookup unavailable")
		}
		out.SellerName, out.Environment, logoKey = seller.Name, seller.Environment, seller.LogoKey
	}
	out.LogoAvailable = logoKey != "" && d.PDFs != nil
	return out, nil
}
