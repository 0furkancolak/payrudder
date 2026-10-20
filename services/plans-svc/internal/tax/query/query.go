// Package query reads the tenant's tax rates.
package query

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Queries reads tax rates.
type Queries struct{ db *store.DB }

// New returns the tax queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Messages maps tax rate rows to API messages.
func Messages(rows []sqlc.ListTaxRatesRow) []*plansv1.TaxRate {
	out := make([]*plansv1.TaxRate, 0, len(rows))
	for _, r := range rows {
		out = append(out, &plansv1.TaxRate{Country: r.Country, Name: r.Name, RateBps: r.RateBps})
	}
	return out
}

// Rates returns the tenant's tax rates.
func (r *Queries) Rates(ctx context.Context) ([]*plansv1.TaxRate, error) {
	var out []*plansv1.TaxRate
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListTaxRates(ctx)
		out = Messages(rows)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "tax rate")
	}
	return out, nil
}
