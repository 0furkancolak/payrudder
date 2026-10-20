// Package query reads plans and plan versions, and what a plan version charges.
package query

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	metersqry "github.com/0furkancolak/quotify/services/plans-svc/internal/meters/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Queries reads plans.
type Queries struct{ db *store.DB }

// New returns the plan queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// List returns the tenant's plans with their latest version number.
func (r *Queries) List(ctx context.Context) ([]*plansv1.Plan, error) {
	var rows []sqlc.ListPlansRow
	err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListPlans(ctx)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "plan")
	}
	var out []*plansv1.Plan
	for _, p := range rows {
		out = append(out, convert.Plan(p.ID.String(), p.Code, p.Name, p.CreatedAt, p.LatestVersion))
	}
	return out, nil
}

// Version returns a plan version, by id or by plan code and version number (0: the latest).
func (r *Queries) Version(ctx context.Context, req *plansv1.GetPlanVersionRequest) (*plansv1.PlanVersion, error) {
	var out *plansv1.PlanVersion
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		scales, err := metersqry.Scales(ctx, q)
		if err != nil {
			return err
		}
		if req.GetPlanVersionId() != "" {
			id, err := uuid.Parse(req.GetPlanVersionId())
			if err != nil {
				return domain.Invalid("plan_version_id", "must be a UUID")
			}
			r, err := q.GetPlanVersionByID(ctx, id)
			if err != nil {
				return err
			}
			out, err = Assemble(ctx, q, r.ID, r.PlanID, r.PlanCode, r.Version, r.BaseFeeMinor, r.PublishedAt, scales)
			return err
		}
		if req.GetPlanCode() == "" {
			return domain.Invalid("plan_code", "plan_version_id or plan_code is required")
		}
		r, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetVersion()})
		if err != nil {
			return err
		}
		out, err = Assemble(ctx, q, r.ID, r.PlanID, r.PlanCode, r.Version, r.BaseFeeMinor, r.PublishedAt, scales)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "plan version")
	}
	return out, nil
}

// Assemble builds the API message of a plan version from its rows, in the caller's transaction.
func Assemble(ctx context.Context, q *sqlc.Queries, id, planID uuid.UUID, planCode string, version int32, baseFee int64,
	published time.Time, scales map[string]int) (*plansv1.PlanVersion, error) {
	pv := &plansv1.PlanVersion{
		Id: id.String(), PlanId: planID.String(), PlanCode: planCode, Version: version, BaseFeeMinor: baseFee,
		PublishTime: timestamppb.New(published),
	}
	fees, err := q.ListPlanVersionFees(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, f := range fees {
		iv := plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
		if f.Interval == string(subscription.Yearly) {
			iv = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
		}
		pv.Fees = append(pv.Fees, &plansv1.CurrencyFee{Currency: f.Currency, BaseFeeMinor: f.BaseFeeMinor, Interval: iv})
	}
	cps, err := q.ListPlanCurrencyPrices(ctx, id)
	if err != nil {
		return nil, err
	}
	byCurrency := map[string]*plansv1.CurrencyPrices{}
	for _, p := range cps {
		price, err := domain.PriceFromStored(p.MeterCode, p.Model, p.Config, scales[p.MeterCode])
		if err != nil {
			return nil, err
		}
		cp, ok := byCurrency[p.Currency]
		if !ok {
			cp = &plansv1.CurrencyPrices{Currency: p.Currency}
			byCurrency[p.Currency] = cp
			pv.CurrencyPrices = append(pv.CurrencyPrices, cp)
		}
		cp.Prices = append(cp.Prices, price)
	}
	prices, err := q.ListPlanPrices(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, p := range prices {
		price, err := domain.PriceFromStored(p.MeterCode, p.Model, p.Config, scales[p.MeterCode])
		if err != nil {
			return nil, err
		}
		pv.Prices = append(pv.Prices, price)
	}
	ents, err := q.ListPlanEntitlements(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		pv.Entitlements = append(pv.Entitlements, &plansv1.Entitlement{
			MeterCode: e.MeterCode, Limit: quantity.FormatUnits(e.LimitUnits, scales[e.MeterCode]), AlertThresholdsPct: e.AlertThresholdsPct,
		})
	}
	return pv, nil
}

// FeeIn returns the fixed fee of a plan version for one billing interval and currency: base_fee_minor for a month in the tenant's
// currency, else the plan version's fee row. A plan version without a fee for the interval and currency cannot be sold that way.
func FeeIn(ctx context.Context, q *sqlc.Queries, planVersionID uuid.UUID, baseFee int64, tenantCurrency, currency string, interval subscription.Interval) (int64, error) {
	if currency == tenantCurrency && interval == subscription.Monthly {
		return baseFee, nil
	}
	fees, err := q.ListPlanVersionFees(ctx, planVersionID)
	if err != nil {
		return 0, err
	}
	for _, f := range fees {
		if f.Currency == currency && f.Interval == string(interval) {
			return f.BaseFeeMinor, nil
		}
	}
	return 0, status.Errorf(codes.FailedPrecondition, "plan version has no %sly fixed fee in %s", interval, currency)
}

// RequireUsagePrices refuses a currency in which the plan version has metered prices but no price set. Fixed-fee-only plan
// versions need nothing beyond their fee.
func RequireUsagePrices(ctx context.Context, q *sqlc.Queries, planVersionID uuid.UUID, tenantCurrency, currency string) error {
	if currency == tenantCurrency {
		return nil
	}
	base, err := q.ListPlanPrices(ctx, planVersionID)
	if err != nil {
		return err
	}
	if len(base) == 0 {
		return nil
	}
	cps, err := q.ListPlanCurrencyPrices(ctx, planVersionID)
	if err != nil {
		return err
	}
	for _, p := range cps {
		if p.Currency == currency {
			return nil
		}
	}
	return status.Errorf(codes.FailedPrecondition, "plan version has metered prices but none in %s", currency)
}
