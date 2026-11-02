// Package command changes the tenant's tax rates.
package command

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	taxqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/query"
)

var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// Commands changes tax rates.
type Commands struct{ db *store.DB }

// New returns the tax commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// SetRates replaces the tenant's tax rates. Rates are checked when saved: a country at most once, a name, a rate between 0 and 100 %.
func (c *Commands) SetRates(ctx context.Context, rates []*plansv1.TaxRate) ([]*plansv1.TaxRate, error) {
	var v domain.Violations
	if len(rates) > 300 {
		v.Add("rates", "at most 300")
	}
	seen := map[string]bool{}
	for i, r := range rates {
		field := fmt.Sprintf("rates[%d]", i)
		switch {
		case r.GetCountry() != "" && !countryCodeRe.MatchString(r.GetCountry()):
			v.Add(field+".country", "an ISO 3166-1 alpha-2 code such as DE, or empty for every other country")
		case seen[r.GetCountry()]:
			v.Add(field+".country", "a country at most once")
		case strings.TrimSpace(r.GetName()) == "" || len(r.GetName()) > 40:
			v.Add(field+".name", "1–40 characters, such as VAT")
		case r.GetRateBps() < 0 || r.GetRateBps() > 10000:
			v.Add(field+".rate_bps", "0..10000 (2000 is 20 %)")
		}
		seen[r.GetCountry()] = true
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out []*plansv1.TaxRate
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		before, err := q.ListTaxRates(ctx)
		if err != nil {
			return err
		}
		if err := q.DeleteTaxRates(ctx); err != nil {
			return err
		}
		for _, r := range rates {
			if err := q.InsertTaxRate(ctx, sqlc.InsertTaxRateParams{Country: r.GetCountry(), Name: strings.TrimSpace(r.GetName()), RateBps: r.GetRateBps()}); err != nil {
				return err
			}
		}
		rows, err := q.ListTaxRates(ctx)
		if err != nil {
			return err
		}
		out = taxqry.Messages(rows)
		return audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "tax_rates.changed", TargetType: "tax_rates", Before: before, After: rows,
			Summary: fmt.Sprintf("set %d tax rates (%d before)", len(rows), len(before)), Detail: map[string]any{"count": len(rows), "previous_count": len(before)}})
	})
	if err != nil {
		return nil, convert.Err(err, "tax rate")
	}
	return out, nil
}
