// Package query reads meters.
package query

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads meters.
type Queries struct{ db *store.DB }

// New returns the meter queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Scales maps each meter code of the tenant to its quantity scale, in the caller's transaction.
func Scales(ctx context.Context, q *sqlc.Queries) (map[string]int, error) {
	meters, err := q.ListLatestMeters(ctx)
	if err != nil {
		return nil, err
	}
	scales := make(map[string]int, len(meters))
	for _, m := range meters {
		scales[m.Code] = int(m.QuantityScale)
	}
	return scales, nil
}

// Get returns the latest version of a meter.
func (r *Queries) Get(ctx context.Context, code string) (*plansv1.Meter, error) {
	var out sqlc.Meter
	err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.LatestMeter(ctx, code)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	m, err := convert.Meter(out)
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	return m, nil
}

// List returns the latest version of every meter.
func (r *Queries) List(ctx context.Context) ([]*plansv1.Meter, error) {
	var rows []sqlc.Meter
	err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListLatestMeters(ctx)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	var out []*plansv1.Meter
	for _, row := range rows {
		m, err := convert.Meter(row)
		if err != nil {
			return nil, convert.Err(err, "meter")
		}
		out = append(out, m)
	}
	return out, nil
}
