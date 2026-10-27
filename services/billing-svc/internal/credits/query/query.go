// Package query reads prepaid credits: packs, a customer's balance and lots, and the ledger. It never writes.
package query

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Packs lists the tenant's packs; activeOnly leaves out the ones withdrawn from sale.
func Packs(ctx context.Context, app pgxx.Beginner, tenantID uuid.UUID, activeOnly bool) ([]model.Pack, error) {
	var out []model.Pack
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		rows, err := sqlc.New(tx).ListCreditPacks(ctx, sqlc.ListCreditPacksParams{TenantID: tenantID, ActiveOnly: activeOnly})
		for _, r := range rows {
			out = append(out, packOf(sqlc.GetCreditPackRow(r)))
		}
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}

func packOf(r sqlc.GetCreditPackRow) model.Pack {
	return model.Pack{ID: r.ID, Name: r.Name, Credits: r.Credits, PriceMinor: r.PriceMinor, Currency: r.Currency, Active: r.Active, CreatedAt: r.CreatedAt, ValidityDays: r.ValidityDays}
}

// PackByID returns one pack, or billing.credits.pack_not_found.
func PackByID(ctx context.Context, app pgxx.Beginner, tenantID, id uuid.UUID) (*model.Pack, error) {
	var out *model.Pack
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		r, err := sqlc.New(tx).GetCreditPack(ctx, sqlc.GetCreditPackParams{TenantID: tenantID, ID: id})
		if err != nil {
			return err
		}
		p := packOf(r)
		out = &p
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "billing.credits.pack_not_found", "no such credit pack")
	}
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}

// Lot is a grant's remaining credits.
type Lot struct {
	ID                 uuid.UUID
	Granted, Remaining int64
	Source             string
	ExpiresAt          *time.Time
	CreatedAt          time.Time
}

// Balance is what a customer can spend now.
type Balance struct {
	Credits    int64
	NextExpiry *time.Time
	Lots       []Lot
}

// BalanceOf returns a customer's spendable credits, when some of them run out, and the lots they come from.
func BalanceOf(ctx context.Context, app pgxx.Beginner, tenantID, customerID uuid.UUID) (*Balance, error) {
	out := &Balance{}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var err error
		if out.Credits, err = q.GetCreditBalance(ctx, sqlc.GetCreditBalanceParams{TenantID: tenantID, CustomerID: customerID}); err != nil {
			return err
		}
		if next, err := q.NextCreditExpiry(ctx, sqlc.NextCreditExpiryParams{TenantID: tenantID, CustomerID: customerID}); err == nil {
			out.NextExpiry = &next
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		lots, err := q.ListCreditLots(ctx, sqlc.ListCreditLotsParams{TenantID: tenantID, CustomerID: customerID})
		for _, l := range lots {
			out.Lots = append(out.Lots, Lot{ID: l.ID, Granted: l.Granted, Remaining: l.Remaining, Source: l.Source, ExpiresAt: l.ExpiresAt, CreatedAt: l.CreatedAt})
		}
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return out, nil
}

// Entry is one row of the ledger.
type Entry struct {
	ID, LotID   uuid.UUID
	Kind        string
	Amount      int64
	Reason, Key string
	// Source is where the credits the entry touched came from: purchase, promo or manual.
	Source    string
	CreatedAt time.Time
}

// Ledger returns a page of a customer's ledger, newest first, and the token of the next page ("" at the end).
func Ledger(ctx context.Context, app pgxx.Beginner, tenantID, customerID uuid.UUID, pageSize int32, token string) ([]Entry, string, error) {
	pageSize, ok := page.Size(pageSize, 50, 200)
	if !ok {
		return nil, "", errkey.New(codes.InvalidArgument, "billing.credits.bad_page_size", "page_size must be 1..200")
	}
	params := sqlc.ListCreditLedgerParams{TenantID: tenantID, CustomerID: customerID, RowLimit: pageSize + 1}
	if token != "" {
		t, id, ok := parseToken(token)
		if !ok {
			return nil, "", errkey.New(codes.InvalidArgument, "billing.credits.bad_page_token", "the page token is not valid")
		}
		params.BeforeTime, params.BeforeID = &t, &id
	}
	var out []Entry
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		rows, err := sqlc.New(tx).ListCreditLedger(ctx, params)
		for _, r := range rows {
			out = append(out, Entry{ID: r.ID, LotID: r.LotID, Kind: r.Kind, Amount: r.Amount, Reason: r.Reason, Key: r.IdempotencyKey, Source: r.Source, CreatedAt: r.CreatedAt})
		}
		return err
	})
	if err != nil {
		return nil, "", apierr.Internal(ctx, err)
	}
	next := ""
	var more bool
	if out, more = page.Trim(out, int(pageSize)); more {
		last := out[len(out)-1]
		next = strconv.FormatInt(last.CreatedAt.UnixMicro(), 10) + "." + last.ID.String()
	}
	return out, next, nil
}

func parseToken(token string) (time.Time, uuid.UUID, bool) {
	micros, id, ok := strings.Cut(token, ".")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	us, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.UnixMicro(us).UTC(), u, true
}

// LowThreshold is the balance below which a consume announces credits.low; 0 means the tenant turned it off (or never set it).
func LowThreshold(ctx context.Context, app pgxx.Beginner, tenantID uuid.UUID) (int64, error) {
	var out int64
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		v, err := sqlc.New(tx).GetCreditSettings(ctx, tenantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		out = v
		return err
	})
	if err != nil {
		return 0, apierr.Internal(ctx, err)
	}
	return out, nil
}
