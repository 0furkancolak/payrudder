package runs

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cockroachdb/apd/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/period"
	runmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// rateRun is what rating one billing run needs about the tenant and the period.
type rateRun struct {
	run       *runmodel.Run
	tctx      context.Context
	tenant    *plansv1.Tenant
	loc       *time.Location
	start     time.Time
	end       time.Time
	env       string
	meters    map[string]inputs.MeterInfo
	subs      map[string]inputs.Subscription
	usage     map[string][]rating.Usage
	taxes     inputs.TaxTable
	plans     map[string]rating.PlanVersion
	lateLines map[string][]lateLine
}

// rate gathers the period's usage, late usage and subscriptions and creates a draft invoice per customer. It is
// idempotent: drafts that exist are kept (the partial unique index one_regular_invoice), so a retried run converges.
func (m *Manager) rate(ctx context.Context, run *runmodel.Run) error {
	rr, err := m.loadRateRun(ctx, run)
	if err != nil {
		return err
	}
	if rr.subs, err = m.in.Subscriptions(ctx, run.TenantID, rr.start, rr.end); err != nil {
		return err
	}
	gen, err := m.streamUsage(ctx, rr)
	if err != nil {
		return err
	}
	late, err := m.streamLateUsage(rr, gen)
	if err != nil {
		return err
	}
	if rr.taxes, err = m.in.TaxRates(ctx, run.TenantID); err != nil {
		return err
	}
	if rr.lateLines, err = m.lateLines(ctx, rr, gen, late); err != nil {
		return err
	}
	if err := m.draftInvoices(ctx, rr, rr.customerIDs(late)); err != nil {
		return err
	}
	if err := m.q.SetRunInvoiceCount(ctx, sqlc.SetRunInvoiceCountParams{TenantID: run.TenantID, PeriodID: run.PeriodID}); err != nil {
		return err
	}
	// The aggregates of the period are final: later on-time-looking arrivals are routed to late usage.
	if _, err := m.in.Aggregator.FreezePeriod(rr.tctx, &aggregatorv1.FreezePeriodRequest{PeriodId: run.PeriodID}); err != nil {
		return fmt.Errorf("freeze period: %w", err)
	}
	return nil
}

// loadRateRun reads the tenant, the period's bounds and the meters.
func (m *Manager) loadRateRun(ctx context.Context, run *runmodel.Run) (*rateRun, error) {
	rr := &rateRun{run: run, tctx: inputs.TenantCtx(ctx, run.TenantID), plans: map[string]rating.PlanVersion{}}
	var err error
	if rr.tenant, err = m.in.Tenant(ctx, run.TenantID); err != nil {
		return nil, err
	}
	if _, ok := money.LookupCurrency(rr.tenant.GetCurrency()); !ok {
		return nil, inputs.Permanent(fmt.Errorf("tenant currency %q is not supported", rr.tenant.GetCurrency()))
	}
	if rr.loc, err = time.LoadLocation(rr.tenant.GetTimezone()); err != nil {
		return nil, inputs.Permanent(err)
	}
	if rr.start, rr.end, err = period.Bounds(run.PeriodID, rr.loc); err != nil {
		return nil, inputs.Permanent(err)
	}
	rr.env = inputs.Environment(rr.tenant)
	if rr.meters, err = m.in.Meters(rr.tctx); err != nil {
		return nil, err
	}
	return rr, nil
}

// customerIDs are the customers with a subscription, usage or late usage in the period, in a stable order.
func (rr *rateRun) customerIDs(late map[lateKey]*apd.Decimal) []string {
	set := map[string]struct{}{}
	for c := range rr.subs {
		set[c] = struct{}{}
	}
	for c := range rr.usage {
		set[c] = struct{}{}
	}
	for k := range late {
		set[k.Customer] = struct{}{}
	}
	ids := make([]string, 0, len(set))
	for c := range set {
		ids = append(ids, c)
	}
	sort.Strings(ids)
	return ids
}

// draftInvoices rates each customer and inserts the drafts a page at a time.
func (m *Manager) draftInvoices(ctx context.Context, rr *rateRun, ids []string) error {
	var drafts []invoicecmd.RegularDraft
	flush := func() error {
		if len(drafts) == 0 {
			return nil
		}
		n, err := m.cfg.Invoices.InsertRegular(ctx, rr.run.TenantID, rr.run.PeriodID, drafts)
		m.cfg.Invoices.Drafted(ctx, int64(n), metric.WithAttributes(attribute.String("environment", rr.env)))
		drafts = drafts[:0]
		return err
	}
	for _, cid := range ids {
		d, err := m.draftFor(ctx, rr, cid)
		if err != nil {
			return err
		}
		if d == nil {
			continue
		}
		drafts = append(drafts, *d)
		if len(drafts) >= m.cfg.PageSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}
