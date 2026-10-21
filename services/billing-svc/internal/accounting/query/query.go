// Package query reads the accounting of a tenant: the expenses as recorded, and the summary of a period. It never writes.
//
// Income is not stored here. It is read from the invoices of billing-svc: an invoice counts on the day its saga reached PAID, its total is the gross income, the
// TAX lines of the invoice are the tax, and credit notes against paid invoices and refunded invoices are the refunds. See docs/phases/phase-37-accounting.md.
package query

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/accounting/model"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Limits of a summary. A day series of more than a year is unreadable, and five years is the longest period a person asks about.
const (
	maxDayRange   = 366
	autoDayRange  = 62
	maxYearsRange = 5
)

// ListParams filter the list of expenses; empty strings are no filter.
type ListParams struct {
	Category, AppID, Currency string
	FromDate, ToDate          string
	IncludeVoided             bool
	PageSize                  int32
	PageToken                 string
}

// List returns a page of expenses, newest first, and the token of the next page ("" at the end).
func List(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p ListParams) ([]model.Expense, string, error) {
	if p.PageSize < 1 {
		p.PageSize = 50
	}
	p.PageSize = min(p.PageSize, 200)
	params := sqlc.ListExpensesParams{TenantID: tenantID, IncludeVoided: p.IncludeVoided, RowLimit: p.PageSize + 1}
	if p.Category != "" {
		if !model.ValidCategory(p.Category) {
			return nil, "", errkey.New(codes.InvalidArgument, "billing.accounting.bad_category", "the category is not one of the known categories")
		}
		params.Category = &p.Category
	}
	if p.Currency != "" {
		params.Currency = &p.Currency
	}
	var err error
	if params.AppID, err = parseApp(p.AppID); err != nil {
		return nil, "", err
	}
	if params.FromDate, err = optionalDay(p.FromDate); err != nil {
		return nil, "", err
	}
	if params.ToDate, err = optionalDay(p.ToDate); err != nil {
		return nil, "", err
	}
	if p.PageToken != "" {
		day, id, ok := parseToken(p.PageToken)
		if !ok {
			return nil, "", errkey.New(codes.InvalidArgument, "billing.accounting.bad_page_token", "the page token is not valid")
		}
		params.BeforeDate, params.BeforeID = model.ToPG(day), &id
	}
	var out []model.Expense
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		rows, err := sqlc.New(tx).ListExpenses(ctx, params)
		for i := range rows {
			r := sqlc.GetExpenseRow(rows[i])
			out = append(out, *model.FromRow(&r))
		}
		return err
	})
	if err != nil {
		return nil, "", internal()
	}
	next := ""
	if int32(len(out)) > p.PageSize { //nolint:gosec // bounded by PageSize+1 <= 201
		out = out[:p.PageSize]
		last := out[len(out)-1]
		next = model.FormatDay(last.IncurredOn) + "." + last.ID.String()
	}
	return out, next, nil
}

// SummaryParams bound a summary. Both days are inside the period.
type SummaryParams struct {
	FromDate, ToDate string
	AppID            string
	Granularity      string
}

// Result is a summary with the period it was computed for.
type Result struct {
	From, To    time.Time
	Granularity string
	AppID       string
	Currencies  []CurrencySummary
}

// Summary returns the income and expenses of the period, per currency.
func Summary(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p SummaryParams) (*Result, error) {
	from, to, gran, err := period(p)
	if err != nil {
		return nil, err
	}
	appID, err := parseApp(p.AppID)
	if err != nil {
		return nil, err
	}
	monthly := gran == GranMonth
	fromAt, toAt := from, to.AddDate(0, 0, 1)
	var (
		income  []IncomeRow
		refunds []RefundRow
		stored  []model.Expense
	)
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		in, err := q.AccountingIncome(ctx, sqlc.AccountingIncomeParams{Monthly: monthly, TenantID: tenantID, FromAt: fromAt, ToAt: toAt})
		if err != nil {
			return err
		}
		for _, r := range in {
			income = append(income, IncomeRow{Bucket: model.FromPG(r.Bucket), Currency: r.Currency, Invoices: r.Invoices, Gross: r.GrossMinor, Tax: r.TaxMinor})
		}
		rf, err := q.AccountingRefunds(ctx, sqlc.AccountingRefundsParams{Monthly: monthly, TenantID: tenantID, FromAt: fromAt, ToAt: toAt})
		if err != nil {
			return err
		}
		for _, r := range rf {
			refunds = append(refunds, RefundRow{Bucket: model.FromPG(r.Bucket), Currency: r.Currency, Refunded: r.RefundedMinor})
		}
		ex, err := q.ExpensesInWindow(ctx, sqlc.ExpensesInWindowParams{TenantID: tenantID, AppID: appID, FromDate: model.ToPG(from), ToDate: model.ToPG(to)})
		if err != nil {
			return err
		}
		for _, r := range ex {
			e := model.Expense{ID: r.ID, Category: r.Category, AmountMinor: r.AmountMinor, Currency: r.Currency, IncurredOn: model.FromPG(r.IncurredOn), Recurring: r.Recurring}
			if u := model.FromPG(r.RecurringUntil); !u.IsZero() {
				e.RecurringUntil = &u
			}
			stored = append(stored, e)
		}
		return nil
	})
	if err != nil {
		return nil, internal()
	}
	res := &Result{From: from, To: to, Granularity: gran, AppID: p.AppID, Currencies: Build(from, to, gran, income, refunds, stored)}
	return res, nil
}

// period validates the dates and chooses the granularity.
func period(p SummaryParams) (from, to time.Time, gran string, err error) {
	bad := errkey.New(codes.InvalidArgument, "billing.accounting.bad_period", "from and to are required days like 2026-10-21, from not after to")
	if from, err = model.ParseDay(p.FromDate); err != nil {
		return from, to, "", bad
	}
	if to, err = model.ParseDay(p.ToDate); err != nil || to.Before(from) {
		return from, to, "", bad
	}
	if to.After(from.AddDate(maxYearsRange, 0, 0)) {
		return from, to, "", errkey.New(codes.InvalidArgument, "billing.accounting.range_too_long", "a period is at most five years")
	}
	days := int(to.Sub(from).Hours()/24) + 1
	switch p.Granularity {
	case "":
		gran = GranMonth
		if days <= autoDayRange {
			gran = GranDay
		}
	case GranDay, GranMonth:
		gran = p.Granularity
	default:
		return from, to, "", errkey.New(codes.InvalidArgument, "billing.accounting.bad_granularity", "granularity is day or month")
	}
	if gran == GranDay && days > maxDayRange {
		return from, to, "", errkey.New(codes.InvalidArgument, "billing.accounting.range_too_long", "a daily series is at most 366 days; use month")
	}
	return from, to, gran, nil
}

func parseApp(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, errkey.New(codes.InvalidArgument, "billing.accounting.bad_app", "app_id is not valid")
	}
	return &id, nil
}

func optionalDay(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	d, err := model.ParseDay(s)
	if err != nil {
		return pgtype.Date{}, errkey.New(codes.InvalidArgument, "billing.accounting.bad_date", "dates look like 2026-10-21")
	}
	return model.ToPG(d), nil
}

func parseToken(token string) (time.Time, uuid.UUID, bool) {
	day, id, ok := strings.Cut(token, ".")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	d, err := model.ParseDay(day)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return d, u, true
}

func internal() error { return status.Error(codes.Internal, "internal error") }
