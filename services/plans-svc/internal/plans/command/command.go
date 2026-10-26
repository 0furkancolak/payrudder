// Package command creates plans and publishes their versions.
package command

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// Commands changes plans.
type Commands struct{ db *store.DB }

// New returns the plan commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// Create adds a plan with no version yet.
func (c *Commands) Create(ctx context.Context, req *plansv1.CreatePlanRequest) (*plansv1.Plan, error) {
	if err := domain.ValidatePlan(req); err != nil {
		return nil, err
	}
	var out sqlc.Plan
	err := c.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.InsertPlan(ctx, sqlc.InsertPlanParams{ID: idgen.NewUUIDv7(), Code: req.GetCode(), Name: req.GetName()})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "plan")
	}
	return convert.Plan(out.ID.String(), out.Code, out.Name, out.CreatedAt, 0), nil
}

// PublishVersion adds the next immutable version of a plan: its fees, prices and entitlements.
func (c *Commands) PublishVersion(ctx context.Context, req *plansv1.PublishPlanVersionRequest) (*plansv1.PlanVersion, error) {
	var out *plansv1.PlanVersion
	publish := func(q *sqlc.Queries) error {
		plan, err := q.GetPlanByCode(ctx, req.GetPlanCode())
		if err != nil {
			return convert.Err(err, "plan")
		}
		scales, err := metersqry.Scales(ctx, q)
		if err != nil {
			return err
		}
		in, err := domain.ValidatePlanVersion(req, scales)
		if err != nil {
			return err
		}
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		fees, err := domain.ValidateFees(t.Currency, req.GetFees())
		if err != nil {
			return err
		}
		currencyPrices, err := domain.ValidateCurrencyPrices(req, scales, fees)
		if err != nil {
			return err
		}
		pv, err := q.InsertPlanVersion(ctx, sqlc.InsertPlanVersionParams{ID: idgen.NewUUIDv7(), PlanID: plan.ID, BaseFeeMinor: in.BaseFeeMinor, TrialDays: in.TrialDays})
		if err != nil {
			return err
		}
		for _, f := range fees {
			if err := q.InsertPlanVersionFee(ctx, sqlc.InsertPlanVersionFeeParams{PlanVersionID: pv.ID, Currency: f.Currency, Interval: f.Interval, BaseFeeMinor: f.BaseFeeMinor}); err != nil {
				return err
			}
		}
		for cur, prices := range currencyPrices {
			for _, p := range prices {
				if err := q.InsertPlanCurrencyPrice(ctx, sqlc.InsertPlanCurrencyPriceParams{PlanVersionID: pv.ID, Currency: cur, MeterCode: p.MeterCode, Model: p.Model, Config: p.Config}); err != nil {
					return err
				}
			}
		}
		for _, p := range in.Prices {
			if err := q.InsertPlanPrice(ctx, sqlc.InsertPlanPriceParams{PlanVersionID: pv.ID, MeterCode: p.MeterCode, Model: p.Model, Config: p.Config}); err != nil {
				return err
			}
		}
		for _, e := range in.Entitlements {
			if err := q.InsertPlanEntitlement(ctx, sqlc.InsertPlanEntitlementParams{
				PlanVersionID: pv.ID, MeterCode: e.MeterCode, LimitUnits: e.LimitUnits, AlertThresholdsPct: e.Thresholds,
				QuotaWindow: e.Window, Mode: e.Mode,
			}); err != nil {
				return err
			}
		}
		out, err = plansqry.Assemble(ctx, q, pv.ID, pv.PlanID, plan.Code, pv.Version, pv.BaseFeeMinor, pv.TrialDays, pv.PublishedAt, scales)
		return err
	}
	// Two concurrent publishes of the same plan collide on UNIQUE (tenant, plan, version); retry once.
	err := c.db.Tx(ctx, publish)
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation && pgxx.ConstraintName(err) == "plan_versions_tenant_id_plan_id_version_key" {
		err = c.db.Tx(ctx, publish)
	}
	if err != nil {
		return nil, convert.Err(err, "plan version")
	}
	return out, nil
}
