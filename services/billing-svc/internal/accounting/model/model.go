// Package model holds what the accounting commands and queries share: the expense as stored, calendar days, and the rule that turns a recurring expense into
// the days it falls on. Everything here is pure (no database, no clock), so the arithmetic is tested without infrastructure.
package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Expense categories. payment_fees is where the provider's commission goes, entered by hand: the payment ledger does not record it.
const (
	CategoryInfrastructure = "infrastructure"
	CategoryPaymentFees    = "payment_fees"
	CategoryMarketing      = "marketing"
	CategoryPayroll        = "payroll"
	CategoryTax            = "tax"
	CategorySoftware       = "software"
	CategoryOther          = "other"
)

// Categories is the list a client may choose from.
var Categories = []string{CategoryInfrastructure, CategoryPaymentFees, CategoryMarketing, CategoryPayroll, CategoryTax, CategorySoftware, CategoryOther}

// Recurrence of an expense.
const (
	RecurNone    = "none"
	RecurMonthly = "monthly"
	RecurYearly  = "yearly"
)

// Expense is a row of the expenses table.
type Expense struct {
	ID             uuid.UUID
	AppID          *uuid.UUID
	Vendor         string
	Category       string
	AmountMinor    int64
	Currency       string
	IncurredOn     time.Time // a UTC day
	Note           string
	Recurring      string
	RecurringUntil *time.Time // a UTC day; nil: no end
	VoidedAt       *time.Time
	VoidReason     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// maxOccurrences stops a pathological row (a monthly expense from year 0001 with no end) from running away: five years of days is far less.
const maxOccurrences = 20_000

// Day returns the UTC day of t, at midnight.
func Day(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// ParseDay reads 2026-10-21.
func ParseDay(s string) (time.Time, error) { return time.ParseInLocation(time.DateOnly, s, time.UTC) }

// FormatDay writes a day as 2026-10-21; the zero time is "".
func FormatDay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.DateOnly)
}

// FromPG converts a database date to a UTC day; an invalid date is the zero time.
func FromPG(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return Day(d.Time)
}

// ToPG converts a UTC day to a database date; the zero time is NULL.
func ToPG(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: Day(t), Valid: true}
}

// MonthStart is the first day of t's month.
func MonthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// addMonths moves start by n months and keeps its day of the month, or the last day of a shorter month: the 31st recurs on the 28th (29th) of February and goes back
// to the 31st in March, it does not drift to the 28th for good. Every occurrence is computed from the start, never from the previous one.
func addMonths(start time.Time, n int) time.Time {
	total := int(start.Month()) - 1 + n
	year := start.Year() + total/12
	month := time.Month(total%12 + 1)
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(start.Day(), last), 0, 0, 0, 0, time.UTC)
}

// Occurrences returns the days an expense falls on inside [from, to], oldest first. A single expense falls on its day; a monthly or yearly one on every
// month or year from its first day until recurring_until (or for ever). All days are UTC days.
func (e *Expense) Occurrences(from, to time.Time) []time.Time {
	start := Day(e.IncurredOn)
	end := Day(to)
	if e.RecurringUntil != nil && Day(*e.RecurringUntil).Before(end) {
		end = Day(*e.RecurringUntil)
	}
	from = Day(from)
	if start.After(end) {
		return nil
	}
	if e.Recurring != RecurMonthly && e.Recurring != RecurYearly {
		if start.Before(from) {
			return nil
		}
		return []time.Time{start}
	}
	step := 1
	if e.Recurring == RecurYearly {
		step = 12
	}
	// Skip the occurrences before the window without visiting them.
	months := (from.Year()-start.Year())*12 + int(from.Month()) - int(start.Month())
	n := max(0, months/step-1)
	var out []time.Time
	for i := 0; i < maxOccurrences; i++ {
		d := addMonths(start, (n+i)*step)
		if d.After(end) {
			break
		}
		if !d.Before(from) {
			out = append(out, d)
		}
	}
	return out
}

// ValidCategory reports whether c is one of Categories.
func ValidCategory(c string) bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// FromRow converts a stored expense; the list query's rows have the same shape and convert with sqlc.GetExpenseRow(row).
func FromRow(r *sqlc.GetExpenseRow) *Expense {
	e := &Expense{ID: r.ID, AppID: r.AppID, Vendor: r.Vendor, Category: r.Category, AmountMinor: r.AmountMinor, Currency: r.Currency, IncurredOn: FromPG(r.IncurredOn),
		Note: r.Note, Recurring: r.Recurring, VoidedAt: r.VoidedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if u := FromPG(r.RecurringUntil); !u.IsZero() {
		e.RecurringUntil = &u
	}
	if r.VoidReason != nil {
		e.VoidReason = *r.VoidReason
	}
	return e
}
