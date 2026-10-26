// Package command changes the entitlement overrides of customers: a customer's own limit on a meter, in place of its plan version's.
package command

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	overridesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/overrides/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes overrides.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the override commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

// customer looks the customer up by external id in the caller's transaction.
func customer(ctx context.Context, q *sqlc.Queries, external string) (sqlc.Customer, error) {
	if external == "" {
		return sqlc.Customer{}, errkey.New(codes.InvalidArgument, "plans.override.customer_required", "customer_external_id is required")
	}
	c, err := q.GetCustomerByExternalID(ctx, external)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, errkey.New(codes.NotFound, "plans.common.not_found", "customer not found")
	}
	return c, err
}

// Set creates or replaces the customer's override on the meter and publishes the entitlements of its subscriptions again, in one transaction.
func (c *Commands) Set(ctx context.Context, req *plansv1.SetEntitlementOverrideRequest) (*plansv1.EntitlementOverride, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.EntitlementOverride
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cust, err := customer(ctx, q, req.GetCustomerExternalId())
		if err != nil {
			return err
		}
		scales, err := metersqry.Scales(ctx, q)
		if err != nil {
			return err
		}
		var v domain.Violations
		scale, known := scales[req.GetMeterCode()]
		if !known {
			v.Add("meter_code", "unknown meter")
			return v.Err()
		}
		se, ok := domain.ValidateEntitlement(domain.EntitlementTerms{MeterCode: req.GetMeterCode(), Limit: req.GetLimit(), Thresholds: req.GetAlertThresholdsPct(),
			Window: req.GetWindow(), Mode: req.GetMode()}, scale, "override", &v)
		if !ok {
			return v.Err()
		}
		now := c.now()
		if se.Mode == "soft" {
			if err := pricedEverywhere(ctx, q, cust, se.MeterCode, now, &v); err != nil {
				return err
			}
			if err := v.Err(); err != nil {
				return err
			}
		}
		row, err := q.UpsertEntitlementOverride(ctx, sqlc.UpsertEntitlementOverrideParams{
			CustomerID: cust.ID, MeterCode: se.MeterCode, LimitUnits: se.LimitUnits, AlertThresholdsPct: se.Thresholds, QuotaWindow: se.Window, Mode: se.Mode,
		})
		if err != nil {
			return err
		}
		out = overridesqry.Of(row, cust.ExternalID, scale)
		return entitlementscmd.Republish(ctx, q, ti.ID, cust.ID, "", now)
	})
	if err != nil {
		return nil, convert.Err(err, "entitlement override")
	}
	return out, nil
}

// pricedEverywhere records a violation when a subscription of the customer that has not ended is on a plan version that does not price the meter: a soft
// limit there would bill nothing over the limit.
func pricedEverywhere(ctx context.Context, q *sqlc.Queries, cust sqlc.Customer, meter string, now time.Time, v *domain.Violations) error {
	subs, err := q.ListUnendedSubscriptionsOfCustomer(ctx, sqlc.ListUnendedSubscriptionsOfCustomerParams{CustomerID: cust.ID, Now: now})
	if err != nil {
		return err
	}
	for _, s := range subs {
		prices, err := q.ListPlanPrices(ctx, s.PlanVersionID)
		if err != nil {
			return err
		}
		priced := false
		for _, p := range prices {
			priced = priced || p.MeterCode == meter
		}
		if !priced {
			v.Add("mode", "QUOTA_MODE_SOFT bills the usage over the limit at the meter's price, and the customer's package does not price "+meter+": use QUOTA_MODE_NOTIFY")
			return nil
		}
	}
	return nil
}

// Delete removes the customer's override on the meter; its subscriptions fall back to their plan version's entitlement (or none) at once.
func (c *Commands) Delete(ctx context.Context, req *plansv1.DeleteEntitlementOverrideRequest) error {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cust, err := customer(ctx, q, req.GetCustomerExternalId())
		if err != nil {
			return err
		}
		n, err := q.DeleteEntitlementOverride(ctx, sqlc.DeleteEntitlementOverrideParams{CustomerID: cust.ID, MeterCode: req.GetMeterCode()})
		if err != nil {
			return err
		}
		if n == 0 {
			return errkey.New(codes.NotFound, "plans.override.not_found", "the customer has no override on that meter")
		}
		return entitlementscmd.Republish(ctx, q, ti.ID, cust.ID, req.GetMeterCode(), c.now())
	})
	return convert.Err(err, "entitlement override")
}
