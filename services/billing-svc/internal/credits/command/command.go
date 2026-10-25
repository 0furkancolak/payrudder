// Package command changes prepaid credits: it sells packs, grants, consumes and expires credits. The rules live here.
//
// A customer's credits are lots, one per grant, each with an optional expiry. Consuming takes from the lot that expires first (lots that never expire go last),
// and every change is a row of the append-only ledger. A customer's credits change one transaction at a time (an advisory lock), so two consumes cannot
// both spend the same credits.
package command

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// Limits. A pack or a movement above maxCredits is a typing mistake, not a business.
const (
	maxCredits = 1_000_000_000_000
	maxKeyLen  = 128
	maxNameLen = 80
	// MaxValidityDays is the longest a pack's credits can stay valid.
	MaxValidityDays = 3650
	maxThreshold    = maxCredits
	maxReason       = 200
	sourcePromo     = "promo"
	// SourceManual and SourcePurchase are the lot sources an operator grant and a paid pack produce.
	SourceManual   = "manual"
	SourcePurchase = "purchase"
)

// Pack is a credit pack as stored.
type Pack struct {
	ID         uuid.UUID
	Name       string
	Credits    int64
	PriceMinor int64
	Currency   string
	Active     bool
	CreatedAt  time.Time
	// ValidityDays is how long the credits bought from the pack last; nil means they never expire.
	ValidityDays *int32
}

// CreatePack adds a pack to the tenant's catalogue.
//
// validityDays is how many days the credits of a purchase last (1..3650); 0 means they never expire.
func CreatePack(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, name string, credits, priceMinor int64, currency string, validityDays int32) (*Pack, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_pack", "a pack needs a name of 1 to 80 characters")
	}
	if credits < 1 || credits > maxCredits || priceMinor < 1 {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_pack", "a pack needs credits and a price above zero")
	}
	if _, ok := money.LookupCurrency(currency); !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.unknown_currency", "the currency is not supported")
	}
	if validityDays < 0 || validityDays > MaxValidityDays {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_validity", "validity_days must be 1 to 3650, or 0 for credits that never expire")
	}
	p := &Pack{ID: uuid.New(), Name: name, Credits: credits, PriceMinor: priceMinor, Currency: currency, Active: true}
	var validity *int32
	if validityDays > 0 {
		validity = &validityDays
		p.ValidityDays = validity
	}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		p.CreatedAt, err = sqlc.New(tx).InsertCreditPack(ctx, sqlc.InsertCreditPackParams{TenantID: tenantID, ID: p.ID, Name: name, Credits: credits, PriceMinor: priceMinor, Currency: currency, ValidityDays: validity})
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return p, nil
}

// SetPackActive withdraws a pack from sale or puts it back.
func SetPackActive(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID, active bool) error {
	var n int64
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		n, err = sqlc.New(tx).SetCreditPackActive(ctx, sqlc.SetCreditPackActiveParams{TenantID: tenantID, ID: id, Active: active})
		return err
	})
	if err != nil {
		return apierr.Internal(ctx, err)
	}
	if n == 0 {
		return errkey.New(codes.NotFound, "billing.credits.pack_not_found", "no such credit pack")
	}
	return nil
}

// Result is what a grant or a consume leaves: the spendable balance, and whether the idempotency key had been used already.
type Result struct {
	Balance  int64
	Replayed bool
}

// Grant adds credits as a promotion or by hand. expires is nil for credits that never expire. A key already used returns the first result and adds nothing.
func Grant(ctx context.Context, app *pgxpool.Pool, tenantID, customerID uuid.UUID, credits int64, source string, expires *time.Time, reason, key string) (*Result, error) {
	if source != sourcePromo && source != SourceManual {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_source", "the source is promo or manual")
	}
	if err := checkAmount(credits, reason, key); err != nil {
		return nil, err
	}
	if expires != nil && !expires.After(time.Now()) {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_expiry", "the expiry must lie in the future")
	}
	out := &Result{}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.LockCustomerCredits(ctx, sqlc.LockCustomerCreditsParams{TenantID: tenantID, CustomerID: customerID}); err != nil {
			return err
		}
		if key != "" {
			prior, err := q.GetLedgerByKey(ctx, sqlc.GetLedgerByKeyParams{TenantID: tenantID, CustomerID: customerID, IdempotencyKey: &key})
			if err != nil {
				return err
			}
			if len(prior) > 0 {
				if prior[0].Kind != "grant" || prior[0].Amount != credits {
					return errkey.New(codes.AlreadyExists, "billing.credits.key_reused", "the idempotency key was used for a different request")
				}
				out.Replayed = true
				return balanceInto(ctx, q, tenantID, customerID, out)
			}
		}
		if err := grantLot(ctx, q, tenantID, customerID, credits, source, nil, expires, reason, key); err != nil {
			return err
		}
		return balanceInto(ctx, q, tenantID, customerID, out)
	})
	if err != nil {
		return nil, wrap(ctx, err)
	}
	return out, nil
}

// GrantPurchase adds the credits of a paid pack inside the caller's transaction. ref identifies the payment (its invoice): a payment grants once, however
// often the confirmation arrives. expires is nil for credits that never expire. It reports whether credits were added.
func GrantPurchase(ctx context.Context, tx pgx.Tx, tenantID, customerID uuid.UUID, credits int64, ref string, expires *time.Time, reason string) (bool, error) {
	q := sqlc.New(tx)
	if err := q.LockCustomerCredits(ctx, sqlc.LockCustomerCreditsParams{TenantID: tenantID, CustomerID: customerID}); err != nil {
		return false, err
	}
	lot, err := q.InsertLot(ctx, sqlc.InsertLotParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, Granted: credits, Source: SourcePurchase, SourceRef: &ref, ExpiresAt: expires})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // this payment was granted before
	}
	if err != nil {
		return false, err
	}
	if reason == "" {
		reason = "credit pack purchase"
	}
	return true, q.InsertLedger(ctx, sqlc.InsertLedgerParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, LotID: lot, Kind: "grant", Amount: credits, Reason: reason})
}

// Purchase is a paid credit-pack invoice, as the saga tells it when the invoice reaches PAID.
type Purchase struct {
	TenantID           uuid.UUID
	CustomerID         uuid.UUID
	CustomerExternalID string
	LinkID             uuid.UUID
	PackID             *uuid.UUID
	PackName           string
	Quantity           int32
	// Credits is what the lot holds: the pack's credits and the bonus a coupon added.
	Credits int64
	// BonusCredits is the part of Credits a coupon added (0 without one).
	BonusCredits int64
	// ValidityDays is 0 for credits that never expire.
	ValidityDays int32
	InvoiceID    uuid.UUID
	InvoiceNo    string
	Currency     string
	AmountMinor  int64
	Now          time.Time
}

// PurchasePaid grants the credits of a paid pack invoice and announces credits.purchased, in the saga's transaction. The lot is keyed by the invoice, so a
// replayed settlement or a duplicate webhook grants nothing more and announces nothing more. An invoice that was voided or refunded before it was paid never
// reaches here: nothing is granted for it.
func PurchasePaid(ctx context.Context, tx pgx.Tx, p Purchase) error {
	var expires *time.Time
	if p.ValidityDays > 0 {
		t := p.Now.AddDate(0, 0, int(p.ValidityDays))
		expires = &t
	}
	reason := "credit pack purchase"
	if p.PackName != "" {
		reason += ": " + p.PackName
		if p.Quantity > 1 {
			reason += " x" + strconv.Itoa(int(p.Quantity))
		}
	}
	granted, err := GrantPurchase(ctx, tx, p.TenantID, p.CustomerID, p.Credits, p.InvoiceID.String(), expires, reason)
	if err != nil || !granted {
		return err
	}
	balance, err := sqlc.New(tx).GetCreditBalance(ctx, sqlc.GetCreditBalanceParams{TenantID: p.TenantID, CustomerID: p.CustomerID})
	if err != nil {
		return err
	}
	data := map[string]any{
		"link_id": p.LinkID.String(), "customer_id": p.CustomerID.String(), "customer_external_id": p.CustomerExternalID, "credits": p.Credits, "quantity": p.Quantity,
		"balance": balance, "invoice_id": p.InvoiceID.String(), "invoice_number": p.InvoiceNo, "currency": p.Currency, "amount_minor": strconv.FormatInt(p.AmountMinor, 10),
		"bonus_credits": p.BonusCredits,
	}
	if p.PackID != nil {
		data["pack_id"] = p.PackID.String()
		data["pack_name"] = p.PackName
	}
	if expires != nil {
		data["expires_at"] = expires.UTC().Format(time.RFC3339)
	}
	return tenantevents.Write(ctx, tx, p.TenantID, tenantevents.CreditsPurchased, p.InvoiceID.String(), p.Now, data)
}

func grantLot(ctx context.Context, q *sqlc.Queries, tenantID, customerID uuid.UUID, credits int64, source string, ref *string, expires *time.Time, reason, key string) error {
	lot, err := q.InsertLot(ctx, sqlc.InsertLotParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, Granted: credits, Source: source, SourceRef: ref, ExpiresAt: expires})
	if err != nil {
		return err
	}
	return q.InsertLedger(ctx, sqlc.InsertLedgerParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, LotID: lot, Kind: "grant", Amount: credits, IdempotencyKey: keyPtr(key), Reason: reason})
}

// Consume takes credits from the lots that expire first. It fails with billing.credits.insufficient and takes nothing when the balance is too small. The key is
// required: a client that retries after a timeout must not be charged twice, and the repeat returns the first result.
//
// When the tenant has set a low-balance threshold and this consume takes the balance from at or above it to below it, credits.low is announced in the same
// transaction (once: a replay returns before reaching that point). externalID names the customer in that event the way the tenant knows them; it may be empty.
func Consume(ctx context.Context, app *pgxpool.Pool, tenantID, customerID uuid.UUID, credits int64, reason, key, externalID string) (*Result, error) {
	if key == "" {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.key_required", "an idempotency key is required to consume credits")
	}
	if err := checkAmount(credits, reason, key); err != nil {
		return nil, err
	}
	out := &Result{}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.LockCustomerCredits(ctx, sqlc.LockCustomerCreditsParams{TenantID: tenantID, CustomerID: customerID}); err != nil {
			return err
		}
		prior, err := q.GetLedgerByKey(ctx, sqlc.GetLedgerByKeyParams{TenantID: tenantID, CustomerID: customerID, IdempotencyKey: &key})
		if err != nil {
			return err
		}
		if len(prior) > 0 {
			var took int64
			for _, e := range prior {
				if e.Kind != "consume" {
					return errkey.New(codes.AlreadyExists, "billing.credits.key_reused", "the idempotency key was used for a different request")
				}
				took -= e.Amount
			}
			if took != credits {
				return errkey.New(codes.AlreadyExists, "billing.credits.key_reused", "the idempotency key was used for a different request")
			}
			out.Replayed = true
			return balanceInto(ctx, q, tenantID, customerID, out)
		}
		lots, err := q.SpendableLots(ctx, sqlc.SpendableLotsParams{TenantID: tenantID, CustomerID: customerID})
		if err != nil {
			return err
		}
		need := credits
		for _, l := range lots {
			if need == 0 {
				break
			}
			take := min(l.Remaining, need)
			if n, err := q.TakeFromLot(ctx, sqlc.TakeFromLotParams{TenantID: tenantID, ID: l.ID, Amount: take}); err != nil || n != 1 {
				if err == nil {
					err = errors.New("credits: a locked lot changed under the lock")
				}
				return err
			}
			if err := q.InsertLedger(ctx, sqlc.InsertLedgerParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, LotID: l.ID, Kind: "consume", Amount: -take, IdempotencyKey: &key, Reason: reason}); err != nil {
				return err
			}
			need -= take
		}
		if need > 0 {
			return errInsufficient // the transaction rolls back: nothing was taken
		}
		if err := balanceInto(ctx, q, tenantID, customerID, out); err != nil {
			return err
		}
		return announceLow(ctx, tx, tenantID, customerID, externalID, key, out.Balance, credits)
	})
	if err != nil {
		return nil, wrap(ctx, err)
	}
	return out, nil
}

// announceLow tells the tenant that a customer's balance crossed below the tenant's threshold. after is the balance now, spent what this consume took.
func announceLow(ctx context.Context, tx pgx.Tx, tenantID, customerID uuid.UUID, externalID, key string, after, spent int64) error {
	threshold, err := sqlc.New(tx).GetCreditSettings(ctx, tenantID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && threshold == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	if after >= threshold || after+spent < threshold {
		return nil // still above it, or already below it before this consume
	}
	return tenantevents.Write(ctx, tx, tenantID, tenantevents.CreditsLow, customerID.String()+"|"+key, time.Now(), map[string]any{
		"customer_id": customerID.String(), "customer_external_id": externalID, "balance": after, "threshold": threshold,
	})
}

// SetLowThreshold sets the balance below which a consume announces credits.low; 0 turns the event off.
func SetLowThreshold(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, threshold int64) error {
	if threshold < 0 || threshold > maxThreshold {
		return errkey.New(codes.InvalidArgument, "billing.credits.bad_threshold", "the low-balance threshold must be 0 or a whole number of credits")
	}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		return sqlc.New(tx).UpsertCreditSettings(ctx, sqlc.UpsertCreditSettingsParams{TenantID: tenantID, LowThreshold: threshold})
	})
	if err != nil {
		return apierr.Internal(ctx, err)
	}
	return nil
}

var errInsufficient = errkey.New(codes.FailedPrecondition, "billing.credits.insufficient", "the customer has too few credits")

// ExpireDue writes off the credits of lots whose time has come, across all tenants, up to limit lots per call, and returns how many it expired. It runs on the
// system pool (billing_system), which sees every tenant.
func ExpireDue(ctx context.Context, system *pgxpool.Pool, limit int32) (int, error) {
	n := 0
	err := pgxx.InTx(ctx, system, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		lots, err := q.ExpiredLots(ctx, limit)
		if err != nil {
			return err
		}
		for _, l := range lots {
			if err := q.InsertLedger(ctx, sqlc.InsertLedgerParams{TenantID: l.TenantID, ID: uuid.New(), CustomerID: l.CustomerID, LotID: l.ID, Kind: "expire", Amount: -l.Remaining, Reason: "expired"}); err != nil {
				return err
			}
			if err := q.ZeroLot(ctx, sqlc.ZeroLotParams{TenantID: l.TenantID, ID: l.ID}); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

func balanceInto(ctx context.Context, q *sqlc.Queries, tenantID, customerID uuid.UUID, out *Result) error {
	b, err := q.GetCreditBalance(ctx, sqlc.GetCreditBalanceParams{TenantID: tenantID, CustomerID: customerID})
	out.Balance = b
	return err
}

func checkAmount(credits int64, reason, key string) error {
	if credits < 1 || credits > maxCredits {
		return errkey.New(codes.InvalidArgument, "billing.credits.bad_amount", "the amount must be a whole number of credits above zero")
	}
	if len(reason) > maxReason {
		return errkey.New(codes.InvalidArgument, "billing.credits.bad_reason", "the reason is too long (at most 200 characters)")
	}
	if len(key) > maxKeyLen {
		return errkey.New(codes.InvalidArgument, "billing.credits.bad_key", "the idempotency key is too long (at most 128 characters)")
	}
	return nil
}

func keyPtr(k string) *string {
	if k == "" {
		return nil
	}
	return &k
}

// wrap passes errors that carry a key through and hides the rest.
func wrap(ctx context.Context, err error) error {
	if errkey.Of(err) != "" {
		return err
	}
	return apierr.Internal(ctx, err)
}
