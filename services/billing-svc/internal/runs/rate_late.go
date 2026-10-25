package runs

import (
	"context"
	"fmt"
	"sort"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	runmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// originBatch is how many keys one call for the usage of the origin periods asks about.
const originBatch = 1000

type lateLine struct {
	line        rating.Line
	planVersion string
	external    string
	currency    string
}

// lateLines prices the late usage billed in this period, incrementally on top of what the origin period (and the late
// batches already billed for it) cost, under the origin period's plan version.
func (m *Manager) lateLines(ctx context.Context, rr *rateRun, gen int32, late map[lateKey]*apd.Decimal) (map[string][]lateLine, error) {
	out := map[string][]lateLine{}
	if len(late) == 0 {
		return out, nil
	}
	run, tenantCurrency := rr.run, rr.tenant.GetCurrency()
	keys, origins := sortedLateKeys(late)
	base, err := m.originUsage(ctx, run.TenantID, gen, keys)
	if err != nil {
		return nil, err
	}
	// Late usage of the same origin that earlier invoices already billed.
	prior, err := m.priorLate(ctx, run, origins)
	if err != nil {
		return nil, err
	}
	// The plan version in force in each origin period.
	subsByOrigin := map[string]map[string]inputs.Subscription{}
	for o := range origins {
		s, e, err := period.Bounds(o, rr.loc)
		if err != nil {
			return nil, inputs.Permanent(err)
		}
		if subsByOrigin[o], err = m.in.Subscriptions(ctx, run.TenantID, s, e); err != nil {
			return nil, err
		}
	}

	for _, k := range keys {
		sub, ok := subsByOrigin[k.Origin][k.Customer]
		if !ok {
			sub, ok = rr.subs[k.Customer]
		}
		if !ok {
			m.noSub.Add(ctx, 1)
			m.log.WarnContext(ctx, "late usage of a customer without a subscription, not invoiced", "customer", k.Customer, "meter", k.Meter, "origin", k.Origin)
			continue
		}
		pv, err := m.in.PlanVersion(ctx, rr.plans, run.TenantID, sub.PlanVersionID, sub.Currency, tenantCurrency)
		if err != nil {
			return nil, err
		}
		already, err := alreadyBilled(base[k], prior[k])
		if err != nil {
			return nil, err
		}
		line, has, err := rating.LateLine(pv, k.Meter, rr.meters[k.Meter].Unit, k.Origin, already, late[k])
		if err != nil {
			return nil, inputs.Permanent(err)
		}
		if has {
			out[k.Customer] = append(out[k.Customer], lateLine{line: line, planVersion: sub.PlanVersionID, external: sub.External, currency: sub.Currency})
		}
	}
	return out, nil
}

// sortedLateKeys are the keys of late in a stable order, and the origin periods they name.
func sortedLateKeys(late map[lateKey]*apd.Decimal) ([]lateKey, map[string]bool) {
	keys := make([]lateKey, 0, len(late))
	origins := map[string]bool{}
	for k := range late {
		keys = append(keys, k)
		origins[k.Origin] = true
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Customer != b.Customer {
			return a.Customer < b.Customer
		}
		if a.Meter != b.Meter {
			return a.Meter < b.Meter
		}
		return a.Origin < b.Origin
	})
	return keys, origins
}

// originUsage is the on-time quantity of each key's origin period.
func (m *Manager) originUsage(ctx context.Context, tenantID uuid.UUID, gen int32, keys []lateKey) (map[lateKey]*apd.Decimal, error) {
	base := map[lateKey]*apd.Decimal{}
	for start := 0; start < len(keys); start += originBatch {
		chunk := keys[start:min(start+originBatch, len(keys))]
		req := &aggregatorv1.GetUsageBatchRequest{Generation: gen}
		for _, k := range chunk {
			req.Keys = append(req.Keys, &aggregatorv1.UsageKey{CustomerId: k.Customer, MeterCode: k.Meter, PeriodId: k.Origin})
		}
		resp, err := m.in.Aggregator.GetUsageBatch(inputs.TenantCtx(ctx, tenantID), req)
		if err != nil {
			return nil, fmt.Errorf("origin usage: %w", err)
		}
		for i, u := range resp.GetUsage() {
			q, err := inputs.Decimal(u.GetQuantity())
			if err != nil {
				return nil, inputs.Permanent(err)
			}
			base[chunk[i]] = q
		}
	}
	return base, nil
}

var apdCtx = apd.BaseContext.WithPrecision(80)

// alreadyBilled is the on-time quantity of an origin period plus the late quantity earlier invoices billed for it.
func alreadyBilled(base, prior *apd.Decimal) (*apd.Decimal, error) {
	if base == nil {
		base = apd.New(0, 0)
	}
	var already apd.Decimal
	if prior == nil {
		already.Set(base)
		return &already, nil
	}
	if _, err := apdCtx.Add(&already, base, prior); err != nil {
		return nil, inputs.Permanent(err)
	}
	return &already, nil
}

// priorLate sums the LATE_USAGE lines of earlier invoices per (customer, meter, origin).
func (m *Manager) priorLate(ctx context.Context, run *runmodel.Run, origins map[string]bool) (map[lateKey]*apd.Decimal, error) {
	list := make([]string, 0, len(origins))
	for o := range origins {
		list = append(list, o)
	}
	rows, err := m.q.PriorLateUsage(ctx, sqlc.PriorLateUsageParams{TenantID: run.TenantID, Origins: list, PeriodID: run.PeriodID})
	if err != nil {
		return nil, err
	}
	out := map[lateKey]*apd.Decimal{}
	for _, r := range rows {
		d, err := inputs.Decimal(r.Quantity)
		if err != nil {
			return nil, inputs.Permanent(err)
		}
		var q apd.Decimal
		if _, err := apdCtx.Quo(&q, d, apd.New(1, int32(r.QuantityScale))); err != nil {
			return nil, inputs.Permanent(err)
		}
		out[lateKey{r.CustomerID, store.Deref(r.MeterCode), store.Deref(r.OriginPeriodID)}] = &q
	}
	return out, nil
}
