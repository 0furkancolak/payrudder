package inputs

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
)

// MeterInfo is a meter as rating needs it: the label of its unit and the scale of the quantities stored for it.
type MeterInfo struct {
	Unit  string
	Scale int
}

// Meters reads the tenant's meters by code; tctx carries the tenant.
func (in *Inputs) Meters(tctx context.Context) (map[string]MeterInfo, error) {
	resp, err := in.Plans.ListMeters(tctx, &plansv1.ListMetersRequest{})
	if err != nil {
		return nil, fmt.Errorf("list meters: %w", err)
	}
	meters := map[string]MeterInfo{}
	for _, mt := range resp.GetMeters() {
		meters[mt.GetCode()] = MeterInfo{Unit: mt.GetUnitLabel(), Scale: int(mt.GetQuantityScale())}
	}
	return meters, nil
}

// PeriodUsage reads the on-time usage of a period, per customer; tctx carries the tenant. generation 0 asks for the tenant's active generation. It returns the
// generation the rows are of (the requested one, or the active one when the period has any usage; 0 when it has none and none was requested).
func (in *Inputs) PeriodUsage(tctx context.Context, periodID string, generation int32, meters map[string]MeterInfo) (map[string][]rating.Usage, int32, error) {
	us, err := in.Aggregator.StreamPeriodUsage(tctx, &aggregatorv1.StreamPeriodUsageRequest{PeriodId: periodID, Generation: generation})
	if err != nil {
		return nil, 0, fmt.Errorf("stream usage: %w", err)
	}
	usage := map[string][]rating.Usage{}
	gen := generation
	for {
		r, err := us.Recv()
		if errors.Is(err, io.EOF) {
			return usage, gen, nil
		}
		if err != nil {
			return nil, 0, fmt.Errorf("stream usage: %w", err)
		}
		u := r.GetUsage()
		if gen == 0 {
			gen = u.GetGeneration()
		}
		q, err := Decimal(u.GetQuantity())
		if err != nil {
			return nil, 0, Permanent(err)
		}
		usage[u.GetCustomerId()] = append(usage[u.GetCustomerId()], rating.Usage{MeterCode: u.GetMeterCode(), Quantity: q, UnitLabel: meters[u.GetMeterCode()].Unit})
	}
}

// TaxTable is a tenant's tax rates by customer country.
type TaxTable map[string]*plansv1.TaxRate

// TaxRates reads the tenant's tax rates.
func (in *Inputs) TaxRates(ctx context.Context, tenantID uuid.UUID) (TaxTable, error) {
	resp, err := in.Plans.GetTaxRates(TenantCtx(ctx, tenantID), &plansv1.GetTaxRatesRequest{})
	if err != nil {
		return nil, fmt.Errorf("get tax rates: %w", err)
	}
	t := TaxTable{}
	for _, r := range resp.GetRates() {
		t[r.GetCountry()] = r
	}
	return t, nil
}

// RateFor is the rate for a customer's country: its own, else the tenant's rate for every other country, else none.
func (t TaxTable) RateFor(country string) (string, int32, bool) {
	if r, ok := t[country]; ok && country != "" {
		return r.GetName(), r.GetRateBps(), true
	}
	if r, ok := t[""]; ok {
		return r.GetName(), r.GetRateBps(), true
	}
	return "", 0, false
}
