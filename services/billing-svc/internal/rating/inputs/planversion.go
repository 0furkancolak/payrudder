package inputs

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
)

// PlanVersion reads a plan version priced in currency, through cache (keyed by plan version and currency).
func (in *Inputs) PlanVersion(ctx context.Context, cache map[string]rating.PlanVersion, tenantID uuid.UUID, id, currency, tenantCurrency string) (rating.PlanVersion, error) {
	key := id + "|" + currency
	if pv, ok := cache[key]; ok {
		return pv, nil
	}
	resp, err := in.Plans.GetPlanVersion(TenantCtx(ctx, tenantID), &plansv1.GetPlanVersionRequest{PlanVersionId: id})
	if err != nil {
		return rating.PlanVersion{}, fmt.Errorf("get plan version %s: %w", id, err)
	}
	pv, err := ConvertPlanVersion(resp.GetPlanVersion(), currency, tenantCurrency)
	if err != nil {
		return rating.PlanVersion{}, Permanent(fmt.Errorf("plan version %s in %s: %w", id, currency, err))
	}
	cache[key] = pv
	return pv, nil
}

// ConvertPlanVersion prices a plan version in currency: the tenant currency uses the base fee and prices, any other the
// per-currency fee and price set the plan version was published with.
func ConvertPlanVersion(p *plansv1.PlanVersion, currency, tenantCurrency string) (rating.PlanVersion, error) {
	pv := rating.PlanVersion{ID: p.GetId(), PlanCode: p.GetPlanCode(), BaseFeeMinor: p.GetBaseFeeMinor(), Prices: map[string]rating.Price{}}
	prices := p.GetPrices()
	if currency != "" && currency != tenantCurrency {
		found := false
		for _, f := range p.GetFees() {
			if f.GetCurrency() == currency {
				pv.BaseFeeMinor, found = f.GetBaseFeeMinor(), true
			}
		}
		if !found {
			return pv, fmt.Errorf("no fixed fee in %s", currency)
		}
		prices = nil
		for _, cp := range p.GetCurrencyPrices() {
			if cp.GetCurrency() == currency {
				prices = cp.GetPrices()
			}
		}
		if len(prices) == 0 && len(p.GetPrices()) > 0 {
			return pv, fmt.Errorf("metered prices but none in %s", currency)
		}
	}
	for _, pr := range prices {
		out, err := convertPrice(pr)
		if err != nil {
			return pv, err
		}
		pv.Prices[pr.GetMeterCode()] = out
	}
	return pv, nil
}

// convertPrice turns one meter's price of a plan version into rating's.
func convertPrice(pr *plansv1.Price) (rating.Price, error) {
	out := rating.Price{MeterCode: pr.GetMeterCode()}
	switch mod := pr.GetModel().(type) {
	case *plansv1.Price_PerUnit:
		out.Model = rating.PerUnit
		d, err := Decimal(mod.PerUnit.GetUnitPrice())
		if err != nil {
			return out, err
		}
		out.UnitPrice = d
	case *plansv1.Price_Tiered:
		out.Model = rating.TieredGraduated
		for _, t := range mod.Tiered.GetTiers() {
			price, err := Decimal(t.GetUnitPrice())
			if err != nil {
				return out, err
			}
			tier := rating.Tier{UnitPrice: price, FlatFeeMinor: t.GetFlatFeeMinor()}
			if t.GetUpTo() != "" {
				if tier.UpTo, err = Decimal(t.GetUpTo()); err != nil {
					return out, err
				}
			}
			out.Tiers = append(out.Tiers, tier)
		}
	case *plansv1.Price_Package:
		out.Model = rating.Package
		size, err := Decimal(mod.Package.GetPackageSize())
		if err != nil {
			return out, err
		}
		out.PackageSize, out.PackagePriceMinor = size, mod.Package.GetPackagePriceMinor()
	default:
		return out, fmt.Errorf("meter %s has no pricing model", pr.GetMeterCode())
	}
	return out, nil
}
