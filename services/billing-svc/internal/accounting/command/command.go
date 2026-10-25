// Package command changes the expenses of a tenant: it records, edits and voids them. The rules live here.
//
// An expense is never deleted. Voiding keeps the row with a reason and takes it out of every summary; editing keeps the row and moves updated_at. A recurring
// expense is one row, edited and voided as a whole.
package command

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/apierr"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Limits. An amount above maxAmount is a typing mistake (ten trillion units of a two-decimal currency), not a business.
const (
	maxAmount   = 1_000_000_000_000_000
	maxVendor   = 120
	maxNote     = 500
	maxReason   = 200
	minYear     = 2000
	maxYearsOut = 50
)

// Input is what a client supplies for an expense; Create and Update validate it the same way.
type Input struct {
	AppID          string
	Vendor         string
	Category       string
	AmountMinor    int64
	Currency       string
	IncurredOn     string
	Note           string
	Recurring      string
	RecurringUntil string
}

type checked struct {
	app        *uuid.UUID
	vendor     string
	note       string
	recurring  string
	incurredOn time.Time
	until      time.Time
}

func (in *Input) check(now time.Time) (*checked, error) {
	c := &checked{vendor: strings.TrimSpace(in.Vendor), note: strings.TrimSpace(in.Note), recurring: in.Recurring}
	if n := utf8.RuneCountInString(c.vendor); n < 1 || n > maxVendor {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "a vendor of 1 to 120 characters is required")
	}
	if utf8.RuneCountInString(c.note) > maxNote {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "the note is at most 500 characters")
	}
	if in.AmountMinor < 1 || in.AmountMinor > maxAmount {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_expense", "the amount must be above zero")
	}
	if !model.ValidCategory(in.Category) {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_category", "the category is not one of the known categories")
	}
	if _, ok := money.LookupCurrency(in.Currency); !ok {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.unknown_currency", "the currency is not supported")
	}
	if c.recurring == "" {
		c.recurring = model.RecurNone
	}
	if c.recurring != model.RecurNone && c.recurring != model.RecurMonthly && c.recurring != model.RecurYearly {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_recurring", "recurring is none, monthly or yearly")
	}
	var err error
	if c.incurredOn, err = parseDay(in.IncurredOn, now); err != nil {
		return nil, err
	}
	if in.RecurringUntil != "" {
		if c.recurring == model.RecurNone {
			return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_recurring", "recurring_until needs a monthly or yearly expense")
		}
		if c.until, err = parseDay(in.RecurringUntil, now); err != nil {
			return nil, err
		}
		if c.until.Before(c.incurredOn) {
			return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_recurring", "recurring_until is before the first day")
		}
	}
	if in.AppID != "" {
		id, err := uuid.Parse(in.AppID)
		if err != nil {
			return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_app", "app_id is not valid")
		}
		c.app = &id
	}
	return c, nil
}

// parseDay reads a day and refuses one so far from today that it is a typing mistake.
func parseDay(s string, now time.Time) (time.Time, error) {
	d, err := model.ParseDay(s)
	if err != nil || d.Year() < minYear || d.Year() > now.Year()+maxYearsOut {
		return time.Time{}, errkey.New(codes.InvalidArgument, "billing.accounting.bad_date", "dates look like 2026-10-21")
	}
	return d, nil
}

func (c *checked) untilPtr() *time.Time {
	if c.until.IsZero() {
		return nil
	}
	return &c.until
}

// Create records an expense.
func Create(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, in *Input) (*model.Expense, error) {
	c, err := in.check(time.Now())
	if err != nil {
		return nil, err
	}
	e := &model.Expense{ID: uuid.New(), AppID: c.app, Vendor: c.vendor, Category: in.Category, AmountMinor: in.AmountMinor, Currency: in.Currency, IncurredOn: c.incurredOn,
		Note: c.note, Recurring: c.recurring, RecurringUntil: c.untilPtr()}
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		row, err := sqlc.New(tx).InsertExpense(ctx, sqlc.InsertExpenseParams{TenantID: tenantID, ID: e.ID, AppID: c.app, Vendor: c.vendor, Category: in.Category,
			AmountMinor: in.AmountMinor, Currency: in.Currency, IncurredOn: model.ToPG(c.incurredOn), Note: c.note, Recurring: c.recurring, RecurringUntil: model.ToPG(c.until)})
		e.CreatedAt, e.UpdatedAt = row.CreatedAt, row.UpdatedAt
		return err
	})
	if err != nil {
		return nil, apierr.Internal(ctx, err)
	}
	return e, nil
}

// Update replaces the fields of an expense that has not been voided.
func Update(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID, in *Input) (*model.Expense, error) {
	c, err := in.check(time.Now())
	if err != nil {
		return nil, err
	}
	var e *model.Expense
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.UpdateExpense(ctx, sqlc.UpdateExpenseParams{TenantID: tenantID, ID: id, AppID: c.app, Vendor: c.vendor, Category: in.Category,
			AmountMinor: in.AmountMinor, Currency: in.Currency, IncurredOn: model.ToPG(c.incurredOn), Note: c.note, Recurring: c.recurring, RecurringUntil: model.ToPG(c.until)}); err != nil {
			return err
		}
		var err error
		e, err = get(ctx, q, tenantID, id)
		return err
	})
	return e, notFoundOrVoided(ctx, app, tenantID, id, err)
}

// Void takes an expense out of every summary. The reason is required: a ledger that loses entries without a word is not one.
func Void(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID, reason string) (*model.Expense, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > maxReason {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.reason_required", "a reason of 1 to 200 characters is required to void an expense")
	}
	var e *model.Expense
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.VoidExpense(ctx, sqlc.VoidExpenseParams{TenantID: tenantID, ID: id, Reason: &reason}); err != nil {
			return err
		}
		var err error
		e, err = get(ctx, q, tenantID, id)
		return err
	})
	return e, notFoundOrVoided(ctx, app, tenantID, id, err)
}

// notFoundOrVoided turns "the update matched no row" into the error the client should see: there is no such expense, or it was voided.
func notFoundOrVoided(ctx context.Context, app *pgxpool.Pool, tenantID, id uuid.UUID, err error) error {
	switch {
	case err == nil:
		return nil
	case !isNoRows(err):
		return apierr.Internal(ctx, err)
	}
	var voided bool
	if lerr := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		row, gerr := sqlc.New(tx).GetExpense(ctx, sqlc.GetExpenseParams{TenantID: tenantID, ID: id})
		if gerr != nil && !isNoRows(gerr) {
			return gerr
		}
		voided = gerr == nil && row.VoidedAt != nil
		return nil
	}); lerr != nil {
		return apierr.Internal(ctx, lerr) // a database failure is not "no such expense"
	}
	if voided {
		return errkey.New(codes.FailedPrecondition, "billing.accounting.expense_voided", "the expense was voided")
	}
	return errkey.New(codes.NotFound, "billing.accounting.expense_not_found", "no such expense")
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func get(ctx context.Context, q *sqlc.Queries, tenantID, id uuid.UUID) (*model.Expense, error) {
	r, err := q.GetExpense(ctx, sqlc.GetExpenseParams{TenantID: tenantID, ID: id})
	if err != nil {
		return nil, err
	}
	return model.FromRow(&r), nil
}
