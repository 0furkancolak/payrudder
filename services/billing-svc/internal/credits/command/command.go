// Package command changes prepaid credits: it sells packs, grants, consumes and expires credits. The rules live here.
//
// A customer's credits are lots, one per grant, each with an optional expiry. Consuming takes from the lot that expires first (lots that never expire go last),
// and every change is a row of the append-only ledger. A customer's credits change one transaction at a time (an advisory lock), so two consumes cannot
// both spend the same credits.
package command

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Limits. A pack or a movement above maxCredits is a typing mistake, not a business.
const (
	maxCredits  = 1_000_000_000_000
	maxKeyLen   = 128
	maxNameLen  = 80
	maxReason   = 200
	sourcePromo = "promo"
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
}

// CreatePack adds a pack to the tenant's catalogue.
func CreatePack(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, name string, credits, priceMinor int64, currency string) (*Pack, error) {
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
	p := &Pack{ID: uuid.New(), Name: name, Credits: credits, PriceMinor: priceMinor, Currency: currency, Active: true}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		p.CreatedAt, err = sqlc.New(tx).InsertCreditPack(ctx, sqlc.InsertCreditPackParams{TenantID: tenantID, ID: p.ID, Name: name, Credits: credits, PriceMinor: priceMinor, Currency: currency})
		return err
	})
	if err != nil {
		return nil, internal(err)
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
		return internal(err)
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
		return nil, wrap(err)
	}
	return out, nil
}

// GrantPurchase adds the credits of a paid pack inside the caller's transaction. ref identifies the payment (its link or invoice): a payment grants once, however
// often the confirmation arrives. It reports whether credits were added.
func GrantPurchase(ctx context.Context, tx pgx.Tx, tenantID, customerID uuid.UUID, credits int64, ref string) (bool, error) {
	q := sqlc.New(tx)
	if err := q.LockCustomerCredits(ctx, sqlc.LockCustomerCreditsParams{TenantID: tenantID, CustomerID: customerID}); err != nil {
		return false, err
	}
	lot, err := q.InsertLot(ctx, sqlc.InsertLotParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, Granted: credits, Source: SourcePurchase, SourceRef: &ref})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // this payment was granted before
	}
	if err != nil {
		return false, err
	}
	return true, q.InsertLedger(ctx, sqlc.InsertLedgerParams{TenantID: tenantID, ID: uuid.New(), CustomerID: customerID, LotID: lot, Kind: "grant", Amount: credits, Reason: "credit pack purchase"})
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
func Consume(ctx context.Context, app *pgxpool.Pool, tenantID, customerID uuid.UUID, credits int64, reason, key string) (*Result, error) {
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
		return balanceInto(ctx, q, tenantID, customerID, out)
	})
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
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
func wrap(err error) error {
	if errkey.Of(err) != "" {
		return err
	}
	return internal(err)
}

func internal(error) error { return status.Error(codes.Internal, "internal error") }
