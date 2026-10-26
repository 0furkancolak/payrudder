// Package query reads the entitlement overrides of customers.
package query

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads overrides.
type Queries struct{ db *store.DB }

// New returns the override queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Of maps a stored override to the API shape; scale is the meter's quantity scale.
func Of(r sqlc.CustomerEntitlementOverride, external string, scale int) *plansv1.EntitlementOverride {
	return &plansv1.EntitlementOverride{
		CustomerExternalId: external, MeterCode: r.MeterCode, Limit: quantity.FormatUnits(r.LimitUnits, scale), AlertThresholdsPct: r.AlertThresholdsPct,
		Window: domain.WindowFromDB(r.QuotaWindow), Mode: domain.ModeFromDB(r.Mode),
		CreateTime: timestamppb.New(r.CreatedAt), UpdateTime: timestamppb.New(r.UpdatedAt),
	}
}

// List returns the customer's overrides, ordered by meter code.
func (r *Queries) List(ctx context.Context, external string) ([]*plansv1.EntitlementOverride, error) {
	out := []*plansv1.EntitlementOverride{}
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		cust, err := q.GetCustomerByExternalID(ctx, external)
		if errors.Is(err, pgx.ErrNoRows) || external == "" {
			return errkey.New(codes.NotFound, "plans.common.not_found", "customer not found")
		}
		if err != nil {
			return err
		}
		rows, err := q.ListEntitlementOverrides(ctx, cust.ID)
		if err != nil {
			return err
		}
		scales, err := metersqry.Scales(ctx, q)
		if err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, Of(row, cust.ExternalID, scales[row.MeterCode]))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "entitlement override")
	}
	return out, nil
}
