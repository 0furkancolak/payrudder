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
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted/query"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
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
	maxPackCredits = int64(1_000_000_000_000)
)

// Link kinds.
const (
	KindInvoice = "invoice"
	KindAdhoc   = "adhoc"
	// KindCreditPack sells a credit pack to one customer. It is a factory like an ad-hoc link (its invoice is made when the payer presses Continue), and when that
	// invoice is paid the saga grants the credits.
	KindCreditPack = "credit_pack"

	// MaxQuantity is the most packs one link can sell.
	MaxQuantity = 1000
)

var promotionCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,40}$`)

// factory reports whether a link of this kind makes an invoice when it is used (rather than being an invoice's link).
func factory(kind string) bool { return kind == KindAdhoc || kind == KindCreditPack }

// Directory is what the commands ask of the rest of the system: customers, the tenant's currency and the invoice of an ad-hoc use (runs.Manager).
type Directory interface {
	Customer(ctx context.Context, tenantID uuid.UUID, externalID string) (*plansv1.Customer, bool, error)
	TenantCurrency(ctx context.Context, tenantID uuid.UUID) (string, error)
	CreateLinkInvoice(ctx context.Context, tenantID uuid.UUID, spec LinkInvoiceSpec) (uuid.UUID, error)
	// QuotePromotion says what the spec's promotion code takes off, claiming nothing.
	QuotePromotion(ctx context.Context, tenantID uuid.UUID, spec LinkInvoiceSpec) (PromotionQuote, error)
	// Attribute tells the affiliate program that a customer who is paying carried a partner's referral code (phase 47). It is best effort: a payment never waits for it
	// to succeed. touched is when the visitor followed the link, the zero time when unknown.
	Attribute(ctx context.Context, tenantID, customerID uuid.UUID, code string, touched time.Time)
}

// Deps are the pools, the saga engine and the directory the commands work with. System is billing_system: every query it runs names the tenant.
type Deps struct {
	App    *pgxpool.Pool
	System *pgxpool.Pool
	Engine *sagacommand.Engine
	Dir    Directory
	// Demo allows the mock payment for live tenants too (PAYRUDDER_DEMO).
	Demo bool
}

// newToken makes an unguessable token: 256 random bits.
func newToken(ctx context.Context) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", apierr.Internal(ctx, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CreateInput is a request for a payment link.
type CreateInput struct {
	// Kind is invoice, adhoc or credit_pack; empty means invoice when InvoiceID is set, else adhoc.
	Kind               string
	InvoiceID          uuid.UUID
	AmountMinor        int64
	Currency           string
	Description        string
	CustomerExternalID string
	// PackID and Quantity are for kind credit_pack: the pack to sell (it must be active) and how many of it (0 means 1).
	PackID   uuid.UUID
	Quantity int32
	// PromotionCode (kinds adhoc and credit_pack) is applied when the payer continues, unless the payer types another.
	PromotionCode  string
	SingleUse      *bool
	ExpiresInDays  int32
	IdempotencyKey string
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
		switch {
		case in.InvoiceID != uuid.Nil:
			in.Kind = KindInvoice
		case in.PackID != uuid.Nil:
			in.Kind = KindCreditPack
		}
	}
	if in.Kind != KindCreditPack && (in.PackID != uuid.Nil || in.Quantity != 0) {
		return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "pack_id and quantity belong to a credit_pack link")
	}
	in.PromotionCode = strings.TrimSpace(in.PromotionCode)
	if in.PromotionCode != "" && (in.Kind == KindInvoice || !promotionCodeRe.MatchString(in.PromotionCode)) {
		return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_promotion_code",
			"promotion_code is 2 to 40 letters, digits, - or _, and belongs to an adhoc or credit_pack link: an invoice that exists is not changed")
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
	case KindCreditPack:
		if in.PackID == uuid.Nil {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.pack_required", "a payment link of kind credit_pack needs pack_id")
		}
		if in.InvoiceID != uuid.Nil || in.AmountMinor != 0 || in.Description != "" || in.Currency != "" {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "a credit_pack link takes its amount, currency and description from the pack; pass pack_id and the customer")
		}
		if in.CustomerExternalID == "" {
			return errkey.New(codes.InvalidArgument, "billing.payment_link.customer_required", "a credit_pack link needs customer_external_id: the credits go to that customer")
		}
		var err error
		if in.Quantity, err = packQuantity(in.Quantity); err != nil {
			return err
		}
	default:
		return errkey.New(codes.InvalidArgument, "billing.payment_link.bad_kind", "kind must be invoice, adhoc or credit_pack")
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
	case in.Kind == KindAdhoc || in.Kind == KindCreditPack:
		return DefaultAdhocDays
	}
	return DefaultInvoiceDays
}

func (in *CreateInput) singleUse() bool {
	if in.SingleUse != nil {
		return *in.SingleUse
	}
	return in.Kind == KindAdhoc || in.Kind == KindCreditPack
}

// fingerprint is what an idempotency key is bound to: the same key with other parameters is a mistake, not a repeat.
func (in *CreateInput) fingerprint() []byte {
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d|%s|%s|%s|%t|%d|%s|%d|%s", in.Kind, in.InvoiceID, in.AmountMinor, in.Currency, in.Description, in.CustomerExternalID, in.singleUse(), in.days(),
		in.PackID, in.Quantity, strings.ToUpper(in.PromotionCode)))
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
				return nil, errkey.New(codes.Unavailable, "billing.hosted.tenant_unavailable", "tenant lookup unavailable")
			}
			in.Currency = c
		}
		if in.CustomerExternalID != "" {
			if _, err := customerOf(ctx, d, tenantID, in.CustomerExternalID, errkey.Key("billing.payment_link.customer_not_found")); err != nil {
				return nil, err
			}
		}
	}
	var customerID uuid.UUID
	if in.Kind == KindCreditPack {
		cu, err := customerOf(ctx, d, tenantID, in.CustomerExternalID, errkey.Key("billing.payment_link.customer_not_found"))
		if err != nil {
			return nil, err
		}
		if customerID, err = uuid.Parse(cu.GetId()); err != nil {
			return nil, apierr.Internal(ctx, err)
		}
	}
	token, err := newToken(ctx)
	if err != nil {
		return nil, err
	}
	expires := time.Now().Add(time.Duration(in.days()) * 24 * time.Hour)
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
			IdempotencyKey: store.NilIfEmpty(in.IdempotencyKey), RequestHash: requestHash(in)}
		switch in.Kind {
		case KindInvoice:
			ext, err := checkInvoice(ctx, q, tenantID, in)
			if err != nil {
				return err
			}
			p.InvoiceID, p.CustomerExternalID = &in.InvoiceID, store.NilIfEmpty(ext)
		case KindCreditPack:
			if err := fillFromPack(ctx, q, tenantID, in, customerID, &p); err != nil {
				return err
			}
		default:
			p.AmountMinor, p.Currency, p.Description = &in.AmountMinor, &in.Currency, &in.Description
			p.CustomerExternalID = store.NilIfEmpty(in.CustomerExternalID)
		}
		if in.PromotionCode != "" {
			// The code must work for this link's own amount, or the payer would meet the refusal at the end. It is claimed only when the payer continues.
			spec := LinkInvoiceSpec{AmountMinor: *p.AmountMinor, Currency: *p.Currency, CustomerExternalID: store.Deref(p.CustomerExternalID), PromotionCode: in.PromotionCode}
			if p.PackID != nil {
				spec.PackID, spec.Credits = p.PackID.String(), *p.Credits
			}
			if _, err := d.Dir.QuotePromotion(ctx, tenantID, spec); err != nil {
				return err
			}
			p.PromotionCode = &in.PromotionCode
		}
		if err := q.InsertPaymentLink(ctx, p); err != nil {
			return err
		}
		row, err := q.GetPaymentLinkRow(ctx, sqlc.GetPaymentLinkRowParams{TenantID: tenantID, ID: id})
		if err != nil {
			return err
		}
		if err := tenantevents.Write(ctx, tx, tenantID, tenantevents.EventPaymentLinkCreated, id.String(), time.Now(), linkData(row)); err != nil {
			return err
		}
		out = &Created{Link: row, Token: token}
		return nil
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation && in.IdempotencyKey != "" && pgxx.ConstraintName(err) == "payment_links_idempotency" {
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
		return nil, apierr.Wrap(ctx, err)
	}
	return out, nil
}

// fillFromPack sets what a credit-pack link sells from the pack itself: the price, the currency, the credits and their validity. None of it comes from the client, so
// a link can never be made cheaper than the pack. A withdrawn pack cannot be sold.
func fillFromPack(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, in CreateInput, customerID uuid.UUID, p *sqlc.InsertPaymentLinkParams) error {
	pack, err := q.GetCreditPack(ctx, sqlc.GetCreditPackParams{TenantID: tenantID, ID: in.PackID})
	if errors.Is(err, pgx.ErrNoRows) {
		return errkey.New(codes.NotFound, "billing.credits.pack_not_found", "no such credit pack")
	}
	if err != nil {
		return err
	}
	if !pack.Active {
		return errkey.New(codes.FailedPrecondition, "billing.credits.pack_inactive", "this credit pack is not on sale")
	}
	q64 := int64(in.Quantity)
	if pack.PriceMinor > maxAmount/q64 || pack.Credits > maxPackCredits/q64 {
		return errkey.New(codes.InvalidArgument, "billing.credits.bad_quantity", "quantity is too large for this pack")
	}
	total, credits := pack.PriceMinor*q64, pack.Credits*q64
	p.AmountMinor, p.Currency, p.Description = &total, &pack.Currency, &pack.Name
	p.CustomerID, p.CustomerExternalID = &customerID, store.NilIfEmpty(in.CustomerExternalID)
	p.PackID, p.Quantity, p.Credits, p.ValidityDays = &in.PackID, &in.Quantity, &credits, pack.ValidityDays
	return nil
}

// customerOf finds the customer a request names. A lookup that fails is Unavailable; a customer there is none of is NotFound under notFoundKey, the key of the
// kind of link the request is about.
func customerOf(ctx context.Context, d Deps, tenantID uuid.UUID, externalID, notFoundKey string) (*plansv1.Customer, error) {
	cu, found, err := d.Dir.Customer(ctx, tenantID, externalID)
	switch {
	case err != nil:
		return nil, errkey.New(codes.Unavailable, "billing.hosted.customer_unavailable", "customer lookup unavailable")
	case !found:
		return nil, errkey.New(codes.NotFound, notFoundKey, "no such customer")
	}
	return cu, nil
}

// packQuantity is how many packs a request buys: none given means one; more than MaxQuantity or fewer than one is refused.
func packQuantity(q int32) (int32, error) {
	if q == 0 {
		q = 1
	}
	if q < 1 || q > MaxQuantity {
		return 0, errkey.New(codes.InvalidArgument, "billing.credits.bad_quantity", "quantity must be 1 to 1000")
	}
	return q, nil
}

// requestHash is what a replay of the idempotency key is compared with: the fingerprint of the request, or nothing when the request carries no key.
func requestHash(in CreateInput) []byte {
	if in.IdempotencyKey == "" {
		return nil
	}
	return in.fingerprint()
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
		return "", errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	}
	if err != nil {
		return "", err
	}
	switch {
	case inv.Status == invmodel.StatusDraft:
		return "", errkey.New(codes.FailedPrecondition, "billing.invoice_link.draft_invoice", "a draft invoice has no number yet and cannot be shared")
	case in.RequireOpen && (inv.Status == invmodel.StatusPaid || inv.Status == invmodel.StatusVoid):
		return "", errkey.New(codes.FailedPrecondition, "billing.payment_link.invoice_not_payable", "a paid or void invoice needs no payment link")
	}
	snap, err := ParseSnapshot(inv.Snapshot)
	if err != nil {
		return "", err
	}
	return snap.CustomerExternalID, nil
}

// ParseSnapshot reads the frozen snapshot of an invoice.
func ParseSnapshot(b []byte) (sagamodel.Snapshot, error) {
	var s sagamodel.Snapshot
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
		return 0, errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	}
	if err != nil {
		return 0, apierr.Internal(ctx, err)
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
		if factory(cur.Kind) {
			if voids, err = q.ChildInvoicesToVoid(ctx, sqlc.ChildInvoicesToVoidParams{TenantID: tenantID, ID: linkID}); err != nil {
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
		return row, apierr.Wrap(ctx, err)
	}
	VoidUnpaid(ctx, d.System, d.Engine, tenantID, voids)
	return row, nil
}

// VoidUnpaid voids invoices through their sagas. An invoice whose saga cannot be voided right now (a payment is in flight) is left as it is: the customer who
// is paying it finishes, and the tenant sees it open.
func VoidUnpaid(ctx context.Context, system *pgxpool.Pool, eng *sagacommand.Engine, tenantID uuid.UUID, ids []uuid.UUID) {
	for _, id := range ids {
		_, err := sagacommand.Void(ctx, system, eng, tenantID, id)
		// Best effort: FailedPrecondition means the saga is busy, NotFound that the invoice has no saga yet. Anything else is worth a line in the log.
		if err != nil && status.Code(err) != codes.FailedPrecondition && status.Code(err) != codes.NotFound {
			slog.WarnContext(ctx, "unpaid invoice not voided", "tenant", tenantID, "invoice", id, "error", err)
		}
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
	cu, lerr := customerOf(ctx, d, tenantID, externalID, errkey.Key("billing.portal_link.customer_not_found"))
	if lerr != nil {
		return "", time.Time{}, uuid.Nil, lerr
	}
	if customerID, err = uuid.Parse(cu.GetId()); err != nil {
		return "", time.Time{}, uuid.Nil, apierr.Internal(ctx, err)
	}
	if token, err = newToken(ctx); err != nil {
		return "", time.Time{}, uuid.Nil, err
	}
	expires = time.Now().Add(time.Duration(days) * 24 * time.Hour)
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return sqlc.New(tx).InsertPortalLink(ctx, sqlc.InsertPortalLinkParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, CustomerExternalID: &externalID, TokenHash: query.TokenHash(token), ExpiresAt: expires})
	})
	if err != nil {
		return "", time.Time{}, uuid.Nil, apierr.Internal(ctx, err)
	}
	return token, expires, customerID, nil
}

// RevokePortalLinks revokes every active portal link of a customer.
func RevokePortalLinks(ctx context.Context, d Deps, tenantID uuid.UUID, externalID string) (int64, error) {
	cu, err := customerOf(ctx, d, tenantID, externalID, errkey.Key("billing.portal_link.customer_not_found"))
	if err != nil {
		return 0, err
	}
	customerID, err := uuid.Parse(cu.GetId())
	if err != nil {
		return 0, apierr.Internal(ctx, err)
	}
	var n int64
	err = pgxx.InTx(ctx, d.System, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		n, err = sqlc.New(tx).RevokePortalLinks(ctx, sqlc.RevokePortalLinksParams{TenantID: tenantID, CustomerID: customerID})
		return err
	})
	if err != nil {
		return 0, apierr.Internal(ctx, err)
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
	if l.PackID != nil {
		d["pack_id"] = l.PackID.String()
	}
	if l.Quantity != nil {
		d["quantity"] = *l.Quantity
	}
	if l.Credits != nil {
		d["credits"] = *l.Credits
	}
	if l.CustomerExternalID != nil {
		d["customer_external_id"] = *l.CustomerExternalID
	}
	return d
}
