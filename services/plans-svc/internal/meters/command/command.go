// Package command changes meters.
package command

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Commands changes meters.
type Commands struct{ db *store.DB }

// New returns the meter commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// Create adds a version of the meter named by the request (the first version of a new code).
func (c *Commands) Create(ctx context.Context, req *plansv1.CreateMeterRequest) (*plansv1.Meter, error) {
	var out sqlc.Meter
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		prevScale, nextVersion := -1, int32(1)
		latest, err := q.LatestMeter(ctx, req.GetCode())
		switch {
		case err == nil:
			prevScale, nextVersion = int(latest.QuantityScale), latest.Version+1
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		in, err := domain.ValidateMeter(req, prevScale)
		if err != nil {
			return err
		}
		if quota := domain.QuotasFor(t.Tier).MaxMeterCodes; prevScale < 0 && quota > 0 {
			n, err := q.CountMeterCodes(ctx)
			if err != nil {
				return err
			}
			if n >= quota {
				return errkey.Newf(codes.ResourceExhausted, "plans.meter.quota_reached", "meter quota of %d reached for tier %s", quota, t.Tier)
			}
		}
		out, err = q.InsertMeter(ctx, sqlc.InsertMeterParams{
			Code: in.Code, Version: nextVersion, Aggregation: in.Aggregation, ValueProperty: in.ValueProperty,
			Filter: in.Filter, QuantityScale: in.Scale, UnitLabel: in.UnitLabel, Category: in.Category,
		})
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
			return errkey.New(codes.Aborted, "plans.meter.changed_concurrently", "concurrent meter version created; retry")
		}
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
