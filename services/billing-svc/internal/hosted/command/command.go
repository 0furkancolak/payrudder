// Package command creates and revokes the public links of invoices (payment links, phase 34, and the customer portal links) and starts the payments the
// pages ask for. The rules for links live here.
package command

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	sagacommand "github.com/0furkancolak/quotify/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Lifetimes of links, in days.
const (
	DefaultInvoiceDays = 90
	DefaultAdhocDays   = 30
	DefaultPortalDays  = 30
	MaxDays            = 365
	// ChildDays is the longest an invoice link handed out by an ad-hoc link works.
	ChildDays = 30

	maxDescription = 200
	maxKey         = 200
	maxAmount      = int64(1) << 50
)

// Link kinds.
const (
	KindInvoice = "invoice"
	KindAdhoc   = "adhoc"
)

// Directory is what the commands ask of the rest of the system: customers, the tenant's currency and the invoice of an ad-hoc use (runs.Manager).
type Directory interface {
	Customer(ctx context.Context, tenantID uuid.UUID, externalID string) (*plansv1.Customer, bool, error)
	TenantCurrency(ctx context.Context, tenantID uuid.UUID) (string, error)
	CreateLinkInvoice(ctx context.Context, tenantID uuid.UUID, spec runs.LinkInvoiceSpec) (uuid.UUID, error)
}

// Deps are the pools, the saga engine and the directory the commands work with. System is billing_system: every query it runs names the tenant.
type Deps struct {
	App    *pgxpool.Pool
	System *pgxpool.Pool
	Engine *saga.Engine
	Dir    Directory
	// Demo allows the mock payment for live tenants too (QUOTIFY_DEMO).
	Demo bool
	Now  func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// NewToken makes an unguessable token: 256 random bits.
func NewToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", status.Error(codes.Internal, "internal error")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func internal(err error) error {
	if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
		return err
	}
	return status.Error(codes.Internal, "internal error")
}

// CreateInput is a request for a payment link.
type CreateInput struct {
	// Kind is invoice or adhoc; empty means invoice when InvoiceID is set, else adhoc.
	Kind               string
	InvoiceID          uuid.UUID
	AmountMinor        int64
	Currency           string
	Description        string
	CustomerExternalID string
	SingleUse          *bool
	ExpiresInDays      int32
	IdempotencyKey     string
	// RequireOpen refuses an invoice that is already paid or void. The phase-23 endpoint (a link to any finalized invoice, a receipt for a paid one) does not.
	RequireOpen bool
}

// Created is a link just made, or the one a repeated idempotency key found.
type Created struct {
	Link     sqlc.GetPaymentLinkRowRow
	Token    string
	Replayed bool
}

func (in *CreateInput) normalize() error {
	in.Description = strings.TrimSpace(in.Description)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if len(in.IdempotencyKey) > maxKey {
		return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_key", "idempotency_key is at most 200 characters")
	}
	if in.Kind == "" {
		in.Kind = KindAdhoc
		if in.InvoiceID != uuid.Nil {
			in.Kind = KindInvoice
		}
	}
	switch in.Kind {
	case KindInvoice:
		if in.InvoiceID == uuid.Nil {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.invoice_required", "a payment link of kind invoice needs invoice_id")
		}
		if in.AmountMinor != 0 || in.Description != "" || in.CustomerExternalID != "" || in.Currency != "" {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "amount, currency, description and customer belong to an ad-hoc link; an invoice link takes them from the invoice")
		}
	case KindAdhoc:
		if in.InvoiceID != uuid.Nil {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "an ad-hoc link has no invoice: the invoice is made when the customer uses it")
		}
		if in.AmountMinor <= 0 || in.AmountMinor > maxAmount {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_amount", "amount_minor must be a positive number of minor units")
		}
		if in.Description == "" || len([]rune(in.Description)) > maxDescription {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_description", "description is required and at most 200 characters")
		}
		if in.Currency != "" {
			if _, ok := money.LookupCurrency(in.Currency); !ok {
				return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_currency", "currency is not supported")
			}
		}
	default:
		return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "kind must be invoice or adhoc")
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > MaxDays {
		return errkey.New(codes.InvalidArgument, "billing.invoice_link.bad_expiry", "expires_in_days must be 1..365")
	}
	return nil
}

func (in *CreateInput) days() int32 {
	switch {
	case in.ExpiresInDays > 0:
		return in.ExpiresInDays
	case in.Kind == KindAdhoc:
		return DefaultAdhocDays
	}
	return DefaultInvoiceDays
}

func (in *CreateInput) singleUse() bool {
	if in.SingleUse != nil {
		return *in.SingleUse
	}
	return in.Kind == KindAdhoc
}

// fingerprint is what an idempotency key is bound to: the same key with other parameters is a mistake, not a repeat.
func (in *CreateInput) fingerprint() []byte {
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d|%s|%s|%s|%t|%d", in.Kind, in.InvoiceID, in.AmountMinor, in.Currency, in.Description, in.CustomerExternalID, in.singleUse(), in.days()))
	return h[:]
}

// CreateLink makes a payment link for a tenant. A repeated idempotency key returns the link made the first time (without its token).
func CreateLink(ctx context.Context, d Deps, tenantID uuid.UUID, in CreateInput) (*Created, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	// What needs other services is settled before the transaction.
	if in.Kind == KindAdhoc {
		if in.Currency == "" {
			c, err := d.Dir.TenantCurrency(ctx, tenantID)
			if err != nil {
				return nil, status.Error(codes.Unavailable, "tenant lookup unavailable")
			}
			in.Currency = c
		}
		if in.CustomerExternalID != "" {
			_, found, err := d.Dir.Customer(ctx, tenantID, in.CustomerExternalID)
			switch {
			case err != nil:
				return nil, status.Error(codes.Unavailable, "customer lookup unavailable")
			case !found:
				return nil, errkey.New(codes.NotFound, "billing.payment_link.customer_not_found", "no such customer")
			}
		}
	}
	token, err := NewToken()
	if err != nil {
		return nil, err
	}
	expires := d.now().Add(time.Duration(in.days()) * 24 * time.Hour)
	id := uuid.New()
	var out *Created
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if in.IdempotencyKey != "" {
			if existing, ok, err := replay(ctx, q, tenantID, in); ok || err != nil {
				out = existing
				return err
			}
		}
		p := sqlc.InsertPaymentLinkParams{TenantID: tenantID, ID: id, Kind: in.Kind, TokenHash: query.TokenHash(token), ExpiresAt: expires, SingleUse: in.singleUse(),
			IdempotencyKey: store.NilIfEmpty(in.IdempotencyKey), RequestHash: nilIfEmptyBytes(in.IdempotencyKey, in.fingerprint())}
		switch in.Kind {
		case KindInvoice:
			ext, err := checkInvoice(ctx, q, tenantID, in)
			if err != nil {
				return err
			}
			p.InvoiceID, p.CustomerExternalID = &in.InvoiceID, store.NilIfEmpty(ext)
		default:
			p.AmountMinor, p.Currency, p.Description = &in.AmountMinor, &in.Currency, &in.Description
			p.CustomerExternalID = store.NilIfEmpty(in.CustomerExternalID)
		}
		if err := q.InsertPaymentLink(ctx, p); err != nil {
			return err
		}
		row, err := q.GetPaymentLinkRow(ctx, sqlc.GetPaymentLinkRowParams{TenantID: tenantID, ID: id})
		if err != nil {
			return err
		}
		if err := saga.TenantEvent(ctx, tx, tenantID, saga.EventPaymentLinkCreated, id.String(), d.now(), linkData(row)); err != nil {
			return err
		}
		out = &Created{Link: row, Token: token}
		return nil
	})
	if pgxx.ErrCode(err) == "23505" && in.IdempotencyKey != "" && pgxx.ConstraintName(err) == "payment_links_idempotency" {
		// Two requests with one key raced: the other one won, and this one is its repeat.
		err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
			existing, ok, err := replay(ctx, sqlc.New(tx), tenantID, in)
			if err == nil && !ok {
				err = errors.New("idempotency key vanished")
			}
			out = existing
			return err
		})
	}
	if err != nil {
		return nil, internal(err)
	}
	return out, nil
}

func nilIfEmptyBytes(key string, b []byte) []byte {
	if key == "" {
		return nil
	}
	return b
}

// replay finds the link an idempotency key made before; ok is false when there is none.
func replay(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, in CreateInput) (*Created, bool, error) {
	prev, err := q.FindPaymentLinkByKey(ctx, sqlc.FindPaymentLinkByKeyParams{TenantID: tenantID, IdempotencyKey: in.IdempotencyKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if string(prev.RequestHash) != string(in.fingerprint()) {
		return nil, false, errkey.New(codes.AlreadyExists, "billing.payment_link.idempotency_conflict", "this idempotency_key was used for a different payment link")
	}
	row, err := q.GetPaymentLinkRow(ctx, sqlc.GetPaymentLinkRowParams{TenantID: tenantID, ID: prev.ID})
	if err != nil {
		return nil, false, err
	}
	return &Created{Link: row, Replayed: true}, true, nil
}

// checkInvoice makes sure the invoice can have a link, and returns its customer's external id.
func checkInvoice(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, in CreateInput) (string, error) {
	inv, err := q.GetInvoiceForLink(ctx, sqlc.GetInvoiceForLinkParams{TenantID: tenantID, ID: in.InvoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return "", err
	}
	switch {
	case inv.Status == "draft":
		return "", errkey.New(codes.FailedPrecondition, "billing.invoice_link.draft_invoice", "a draft invoice has no number yet and cannot be shared")
	case in.RequireOpen && (inv.Status == "paid" || inv.Status == "void"):
		return "", errkey.New(codes.FailedPrecondition, "billing.payment_link.invoice_not_payable", "a paid or void invoice needs no payment link")
	}
	snap, err := ParseSnapshot(inv.Snapshot)
	if err != nil {
		return "", err
	}
	return snap.CustomerExternalID, nil
}

// ParseSnapshot reads the frozen snapshot of an invoice.
func ParseSnapshot(b []byte) (saga.Snapshot, error) {
	var s saga.Snapshot
	err := json.Unmarshal(b, &s)
	return s, err
}

// CreateInvoiceLink is the phase-23 operation: a link to a finalized invoice. It returns the token and the expiry.
func CreateInvoiceLink(ctx context.Context, d Deps, tenantID, invoiceID uuid.UUID, expiresInDays int32) (string, time.Time, error) {
	c, err := CreateLink(ctx, d, tenantID, CreateInput{Kind: KindInvoice, InvoiceID: invoiceID, ExpiresInDays: expiresInDays})
	if err != nil {
		return "", time.Time{}, err
	}
	return c.Token, c.Link.ExpiresAt, nil
}

// RevokeLinks revokes every active link of an invoice and returns how many there were.
func RevokeLinks(ctx context.Context, d Deps, tenantID, invoiceID uuid.UUID) (int64, error) {
	var n int64
	err := pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetInvoiceTotals(ctx, sqlc.GetInvoiceTotalsParams{TenantID: tenantID, ID: invoiceID}); err != nil {
			return err
		}
		var err error
		n, err = q.RevokeInvoiceLinks(ctx, sqlc.RevokeInvoiceLinksParams{TenantID: tenantID, InvoiceID: &invoiceID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return 0, status.Error(codes.Internal, "internal error")
	}
	return n, nil
}

// RevokeLink revokes a payment link, the invoice links it handed out, and voids the invoices an ad-hoc link made that nobody has paid (nobody can any more).
// Revoking a revoked link changes nothing.
func RevokeLink(ctx context.Context, d Deps, tenantID, linkID uuid.UUID) (sqlc.GetPaymentLinkRowRow, error) {
	var voids []uuid.UUID
	var row sqlc.GetPaymentLinkRowRow
	err := pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.GetPaymentLinkRow(ctx, sqlc.GetPaymentLinkRowParams{TenantID: tenantID, ID: linkID})
		if err != nil {
			return err
		}
		if cur.Kind == KindAdhoc {
			if voids, err = unpaidInvoices(ctx, q, tenantID, linkID); err != nil {
				return err
			}
		}
		if _, err := q.RevokePaymentLink(ctx, sqlc.RevokePaymentLinkParams{TenantID: tenantID, ID: linkID}); err != nil {
			return err
		}
		row, err = q.GetPaymentLinkRow(ctx, sqlc.GetPaymentLinkRowParams{TenantID: tenantID, ID: linkID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errkey.New(codes.NotFound, "billing.payment_link.not_found", "no such payment link")
	}
	if err != nil {
		return row, internal(err)
	}
	VoidUnpaid(ctx, d.System, d.Engine, tenantID, voids)
	return row, nil
}

func unpaidInvoices(ctx context.Context, q *sqlc.Queries, tenantID, linkID uuid.UUID) ([]uuid.UUID, error) {
	return q.ChildInvoicesToVoid(ctx, sqlc.ChildInvoicesToVoidParams{TenantID: tenantID, ID: linkID})
}

// UnpaidInvoices lists the open invoices an ad-hoc link made.
func UnpaidInvoices(ctx context.Context, q *sqlc.Queries, tenantID, linkID uuid.UUID) ([]uuid.UUID, error) {
	return unpaidInvoices(ctx, q, tenantID, linkID)
}

// VoidUnpaid voids invoices through their sagas. An invoice whose saga cannot be voided right now (a payment is in flight) is left as it is: the customer who
// is paying it finishes, and the tenant sees it open.
func VoidUnpaid(ctx context.Context, system *pgxpool.Pool, eng *saga.Engine, tenantID uuid.UUID, ids []uuid.UUID) {
	for _, id := range ids {
		_, _ = sagacommand.Void(ctx, system, eng, tenantID, id) // best effort: FailedPrecondition means the saga is busy
	}
}

// CreatePortalLink makes the link to a customer's payment page.
func CreatePortalLink(ctx context.Context, d Deps, tenantID uuid.UUID, externalID string, days int32) (token string, expires time.Time, customerID uuid.UUID, err error) {
	switch {
	case days == 0:
		days = DefaultPortalDays
	case days < 0 || days > MaxDays:
		return "", time.Time{}, uuid.Nil, errkey.New(codes.InvalidArgument, "billing.invoice_link.bad_expiry", "expires_in_days must be 1..365")
	}
	if externalID == "" {
		return "", time.Time{}, uuid.Nil, errkey.New(codes.InvalidArgument, "billing.portal_link.customer_required", "customer_external_id is required")
	}
	cu, found, lerr := d.Dir.Customer(ctx, tenantID, externalID)
	switch {
	case lerr != nil:
		return "", time.Time{}, uuid.Nil, status.Error(codes.Unavailable, "customer lookup unavailable")
	case !found:
		return "", time.Time{}, uuid.Nil, errkey.New(codes.NotFound, "billing.portal_link.customer_not_found", "no such customer")
	}
	if customerID, err = uuid.Parse(cu.GetId()); err != nil {
		return "", time.Time{}, uuid.Nil, status.Error(codes.Internal, "internal error")
	}
	if token, err = NewToken(); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	expires = d.now().Add(time.Duration(days) * 24 * time.Hour)
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return sqlc.New(tx).InsertPortalLink(ctx, sqlc.InsertPortalLinkParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, TokenHash: query.TokenHash(token), ExpiresAt: expires})
	})
	if err != nil {
		return "", time.Time{}, uuid.Nil, status.Error(codes.Internal, "internal error")
	}
	return token, expires, customerID, nil
}

// RevokePortalLinks revokes every active portal link of a customer.
func RevokePortalLinks(ctx context.Context, d Deps, tenantID uuid.UUID, externalID string) (int64, error) {
	cu, found, err := d.Dir.Customer(ctx, tenantID, externalID)
	switch {
	case err != nil:
		return 0, status.Error(codes.Unavailable, "customer lookup unavailable")
	case !found:
		return 0, errkey.New(codes.NotFound, "billing.portal_link.customer_not_found", "no such customer")
	}
	customerID, err := uuid.Parse(cu.GetId())
	if err != nil {
		return 0, status.Error(codes.Internal, "internal error")
	}
	var n int64
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		n, err = sqlc.New(tx).RevokePortalLinks(ctx, sqlc.RevokePortalLinksParams{TenantID: tenantID, CustomerID: customerID})
		return err
	})
	if err != nil {
		return 0, status.Error(codes.Internal, "internal error")
	}
	return n, nil
}

func linkData(l sqlc.GetPaymentLinkRowRow) map[string]any {
	d := map[string]any{"link_id": l.ID.String(), "kind": l.Kind, "single_use": l.SingleUse, "expires_at": l.ExpiresAt.UTC().Format(time.RFC3339)}
	if l.InvoiceID != nil {
		d["invoice_id"] = l.InvoiceID.String()
	}
	if l.AmountMinor != nil {
		d["amount_minor"] = strconv.FormatInt(*l.AmountMinor, 10)
	}
	if l.Currency != nil {
		d["currency"] = *l.Currency
	}
	if l.Description != nil {
		d["description"] = *l.Description
	}
	if l.CustomerExternalID != nil {
		d["customer_external_id"] = *l.CustomerExternalID
	}
	return d
}
