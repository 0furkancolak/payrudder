package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rating"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

type meterInfo struct {
	Unit  string
	Scale int
}

type subscription struct {
	ID, CustomerID, External string
	PlanVersionID            string
	Start                    time.Time
	Currency                 string
	ChangedFromID            string
	TrialEnd, PausedAt       *time.Time
	Paused                   bool
	// Provider, ProviderRef and Owner: the payment provider that sells the subscription and, when it runs it, its id there. A provider-owned
	// subscription's fixed fee is billed by the provider, so it is not on Quotify's invoice.
	Provider, ProviderRef, Owner string
	// AdvanceFee: the fixed fee is invoiced at the start of each billing period (kind FEE), not with the month's usage.
	AdvanceFee bool
	// Chain is this subscription and the ones it replaced by plan changes inside the same period, oldest first. A
	// subscription that was not changed has a chain of one.
	Chain []subscription
}

// subscriptions returns, per customer, the subscription that was active last in the period, with the plan changes
// that led to it inside the period as its Chain. Usage is priced at the last plan; the fixed fee is split between the
// plans of the chain (rating.FixedFeeLines).
func (m *Manager) subscriptions(ctx context.Context, tenantID uuid.UUID, start, end time.Time) (map[string]subscription, error) {
	stream, err := m.cfg.Plans.ListSubscriptionsForPeriod(tenantCtx(ctx, tenantID), &plansv1.ListSubscriptionsForPeriodRequest{
		PeriodStart: timestampOf(start), PeriodEnd: timestampOf(end)})
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	all := map[string][]subscription{}
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		s := r.GetSubscription()
		sub := subscription{ID: s.GetId(), CustomerID: s.GetCustomerId(), External: s.GetCustomerExternalId(),
			PlanVersionID: s.GetPlanVersionId(), Start: s.GetStartTime().AsTime(), Currency: s.GetCurrency(),
			ChangedFromID: s.GetChangedFromId(), Paused: s.GetState() == plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED,
			Provider: s.GetProvider(), ProviderRef: s.GetProviderRef(), Owner: s.GetOwner(),
			AdvanceFee: s.GetFeeTiming() == plansv1.FeeTiming_FEE_TIMING_ADVANCE}
		if s.GetTrialEndTime() != nil {
			t := s.GetTrialEndTime().AsTime()
			sub.TrialEnd = &t
		}
		if s.GetPauseTime() != nil {
			t := s.GetPauseTime().AsTime()
			sub.PausedAt = &t
		}
		all[sub.CustomerID] = append(all[sub.CustomerID], sub)
	}
	out := map[string]subscription{}
	for cid, subs := range all {
		out[cid] = lastWithChain(subs)
	}
	return out, nil
}

// lastWithChain picks the latest-started subscription and walks back through ChangedFromID while the predecessor is in
// the period too.
func lastWithChain(subs []subscription) subscription {
	byID := make(map[string]subscription, len(subs))
	last := subs[0]
	for _, s := range subs {
		byID[s.ID] = s
		if s.Start.After(last.Start) {
			last = s
		}
	}
	chain := []subscription{last}
	for cur := last; cur.ChangedFromID != ""; {
		prev, ok := byID[cur.ChangedFromID]
		if !ok {
			break
		}
		chain = append([]subscription{prev}, chain...)
		cur = prev
	}
	last.Chain = chain
	return last
}

func (m *Manager) planVersion(ctx context.Context, cache map[string]rating.PlanVersion, tenantID uuid.UUID, id, currency, tenantCurrency string) (rating.PlanVersion, error) {
	key := id + "|" + currency
	if pv, ok := cache[key]; ok {
		return pv, nil
	}
	resp, err := m.cfg.Plans.GetPlanVersion(tenantCtx(ctx, tenantID), &plansv1.GetPlanVersionRequest{PlanVersionId: id})
	if err != nil {
		return rating.PlanVersion{}, fmt.Errorf("get plan version %s: %w", id, err)
	}
	pv, err := convertPlanVersion(resp.GetPlanVersion(), currency, tenantCurrency)
	if err != nil {
		return rating.PlanVersion{}, permanent{fmt.Errorf("plan version %s in %s: %w", id, currency, err)}
	}
	cache[key] = pv
	return pv, nil
}

// orDefault returns v, or def when v is empty: a subscription without a currency is in the tenant's.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func decimal(s string) (*apd.Decimal, error) {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return d, nil
}

// convertPlanVersion prices a plan version in currency: the tenant currency uses the base fee and prices, any other the
// per-currency fee and price set the plan version was published with.
func convertPlanVersion(p *plansv1.PlanVersion, currency, tenantCurrency string) (rating.PlanVersion, error) {
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
		out := rating.Price{MeterCode: pr.GetMeterCode()}
		switch mod := pr.GetModel().(type) {
		case *plansv1.Price_PerUnit:
			out.Model = rating.PerUnit
			d, err := decimal(mod.PerUnit.GetUnitPrice())
			if err != nil {
				return pv, err
			}
			out.UnitPrice = d
		case *plansv1.Price_Tiered:
			out.Model = rating.TieredGraduated
			for _, t := range mod.Tiered.GetTiers() {
				price, err := decimal(t.GetUnitPrice())
				if err != nil {
					return pv, err
				}
				tier := rating.Tier{UnitPrice: price, FlatFeeMinor: t.GetFlatFeeMinor()}
				if t.GetUpTo() != "" {
					if tier.UpTo, err = decimal(t.GetUpTo()); err != nil {
						return pv, err
					}
				}
				out.Tiers = append(out.Tiers, tier)
			}
		case *plansv1.Price_Package:
			out.Model = rating.Package
			size, err := decimal(mod.Package.GetPackageSize())
			if err != nil {
				return pv, err
			}
			out.PackageSize, out.PackagePriceMinor = size, mod.Package.GetPackagePriceMinor()
		default:
			return pv, fmt.Errorf("meter %s has no pricing model", pr.GetMeterCode())
		}
		pv.Prices[pr.GetMeterCode()] = out
	}
	return pv, nil
}

// feeSegments splits the period between the plan versions of the subscription's chain. The first plan is in force from the
// start of the period even if it began inside it (a subscription pays its whole first month), each later plan from the
// instant it replaced the one before. A segment is waived while a trial runs past the period, or when the subscription was
// paused before the period began.
func (m *Manager) feeSegments(ctx context.Context, plans map[string]rating.PlanVersion, tenantID uuid.UUID, sub subscription,
	start, end time.Time, tenantCurrency string) ([]rating.FeeSegment, error) {
	var segs []rating.FeeSegment
	for i, member := range sub.Chain {
		pv, err := m.planVersion(ctx, plans, tenantID, member.PlanVersionID, member.Currency, tenantCurrency)
		if err != nil {
			return nil, err
		}
		from := start
		if i > 0 && member.Start.After(start) {
			from = member.Start
		}
		to := end
		if i+1 < len(sub.Chain) && sub.Chain[i+1].Start.Before(end) {
			to = sub.Chain[i+1].Start
		}
		if to.Before(from) {
			to = from
		}
		segs = append(segs, rating.FeeSegment{PlanCode: pv.PlanCode, FeeMinor: pv.BaseFeeMinor, From: from, To: to, Waived: feeWaived(member, start, end)})
	}
	return segs, nil
}

// feeWaived reports whether the fixed fee of the period is not on the month's invoice: a trial runs past the period, the subscription was paused before it
// began, the provider bills the fee, or it is invoiced in advance on its own.
func feeWaived(member subscription, start, end time.Time) bool {
	trialRunsPast := member.TrialEnd != nil && member.TrialEnd.After(end)
	pausedBefore := member.Paused && member.PausedAt != nil && !member.PausedAt.After(start)
	return trialRunsPast || pausedBefore || member.Owner == "provider" || member.AdvanceFee
}

type lateKey struct{ Customer, Meter, Origin string }

// draftInvoice is an invoice about to be inserted.
type draftInvoice struct {
	// Provider and Country are what reports split by: the subscription's payment provider and the customer's country.
	Provider, Country string
	Currency          string
	CustomerID        uuid.UUID
	PlanVersion       uuid.UUID
	Lines             []rating.Line
	LineScales        []int
	Total             int64
	Snapshot          saga.Snapshot
}

// rateRun is what rating one billing run needs about the tenant and the period.
type rateRun struct {
	run       *Run
	tctx      context.Context
	tenant    *plansv1.Tenant
	loc       *time.Location
	start     time.Time
	end       time.Time
	env       string
	logoKey   string
	meters    map[string]meterInfo
	subs      map[string]subscription
	usage     map[string][]rating.Usage
	taxes     taxTable
	plans     map[string]rating.PlanVersion
	lateLines map[string][]lateLine
}

// rate gathers the period's usage, late usage and subscriptions and creates a draft invoice per customer. It is
// idempotent: drafts that exist are kept (the partial unique index one_regular_invoice), so a retried run converges.
func (m *Manager) rate(ctx context.Context, run *Run) error {
	rr, err := m.loadRateRun(ctx, run)
	if err != nil {
		return err
	}
	if rr.subs, err = m.subscriptions(ctx, run.TenantID, rr.start, rr.end); err != nil {
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
	if rr.taxes, err = m.taxRates(ctx, run.TenantID); err != nil {
		return err
	}
	if rr.lateLines, err = m.lateLines(ctx, run, gen, late, rr.subs, rr.meters, rr.plans, rr.loc, rr.tenant.GetCurrency()); err != nil {
		return err
	}
	if err := m.draftInvoices(ctx, rr, rr.customerIDs(late)); err != nil {
		return err
	}
	if err := m.q.SetRunInvoiceCount(ctx, sqlc.SetRunInvoiceCountParams{TenantID: run.TenantID, PeriodID: run.PeriodID}); err != nil {
		return err
	}
	// The aggregates of the period are final: later on-time-looking arrivals are routed to late usage.
	if _, err := m.cfg.Aggregator.FreezePeriod(rr.tctx, &aggregatorv1.FreezePeriodRequest{PeriodId: run.PeriodID}); err != nil {
		return fmt.Errorf("freeze period: %w", err)
	}
	return nil
}

// loadRateRun reads the tenant, the period's bounds and the meters.
func (m *Manager) loadRateRun(ctx context.Context, run *Run) (*rateRun, error) {
	rr := &rateRun{run: run, tctx: tenantCtx(ctx, run.TenantID), usage: map[string][]rating.Usage{}, plans: map[string]rating.PlanVersion{}}
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: run.TenantID.String()})
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	rr.tenant = tn.GetTenant()
	if _, ok := money.LookupCurrency(rr.tenant.GetCurrency()); !ok {
		return nil, permanent{fmt.Errorf("tenant currency %q is not supported", rr.tenant.GetCurrency())}
	}
	if rr.loc, err = time.LoadLocation(rr.tenant.GetTimezone()); err != nil {
		return nil, permanent{err}
	}
	if rr.start, rr.end, err = period.Bounds(run.PeriodID, rr.loc); err != nil {
		return nil, permanent{err}
	}
	rr.env = environmentOf(rr.tenant)
	rr.logoKey = logoKeyOf(run.TenantID, rr.tenant)
	if rr.meters, err = m.meters(rr.tctx); err != nil {
		return nil, err
	}
	return rr, nil
}

func environmentOf(t *plansv1.Tenant) string {
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
		return "sandbox"
	}
	return "live"
}

// logoKeyOf is the object key of the tenant's invoice logo, an assets-svc object under {tenant}/assets/{asset id}.
func logoKeyOf(tenantID uuid.UUID, t *plansv1.Tenant) string {
	if id := t.GetInvoiceLogoAssetId(); id != "" {
		return tenantID.String() + "/assets/" + id
	}
	return ""
}

func (m *Manager) meters(tctx context.Context) (map[string]meterInfo, error) {
	resp, err := m.cfg.Plans.ListMeters(tctx, &plansv1.ListMetersRequest{})
	if err != nil {
		return nil, fmt.Errorf("list meters: %w", err)
	}
	meters := map[string]meterInfo{}
	for _, mt := range resp.GetMeters() {
		meters[mt.GetCode()] = meterInfo{Unit: mt.GetUnitLabel(), Scale: int(mt.GetQuantityScale())}
	}
	return meters, nil
}

// streamUsage reads the on-time usage of the period at the generation the run pins, pinning the tenant's active one when the run has none yet.
func (m *Manager) streamUsage(ctx context.Context, rr *rateRun) (int32, error) {
	run := rr.run
	gen := int32(0)
	if run.Generation != nil {
		gen = *run.Generation
	}
	us, err := m.cfg.Aggregator.StreamPeriodUsage(rr.tctx, &aggregatorv1.StreamPeriodUsageRequest{PeriodId: run.PeriodID, Generation: gen})
	if err != nil {
		return 0, fmt.Errorf("stream usage: %w", err)
	}
	for {
		r, err := us.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("stream usage: %w", err)
		}
		u := r.GetUsage()
		if gen == 0 {
			gen = u.GetGeneration()
		}
		q, err := decimal(u.GetQuantity())
		if err != nil {
			return 0, permanent{err}
		}
		rr.usage[u.GetCustomerId()] = append(rr.usage[u.GetCustomerId()], rating.Usage{MeterCode: u.GetMeterCode(), Quantity: q, UnitLabel: rr.meters[u.GetMeterCode()].Unit})
	}
	if gen == 0 {
		gen = 1 // no usage rows: the tenant has only ever had generation 1
	}
	if run.Generation == nil {
		if err := m.q.SetRunGeneration(ctx, sqlc.SetRunGenerationParams{Generation: gen, TenantID: run.TenantID, PeriodID: run.PeriodID}); err != nil {
			return 0, err
		}
		run.Generation = &gen
	}
	return gen, nil
}

// streamLateUsage reads the late usage billed in this period.
func (m *Manager) streamLateUsage(rr *rateRun, gen int32) (map[lateKey]*apd.Decimal, error) {
	late := map[lateKey]*apd.Decimal{}
	ls, err := m.cfg.Aggregator.StreamLateUsage(rr.tctx, &aggregatorv1.StreamLateUsageRequest{BilledPeriodId: rr.run.PeriodID, Generation: gen})
	if err != nil {
		return nil, fmt.Errorf("stream late usage: %w", err)
	}
	for {
		r, err := ls.Recv()
		if errors.Is(err, io.EOF) {
			return late, nil
		}
		if err != nil {
			return nil, fmt.Errorf("stream late usage: %w", err)
		}
		l := r.GetLateUsage()
		q, err := decimal(l.GetQuantity())
		if err != nil {
			return nil, permanent{err}
		}
		late[lateKey{l.GetCustomerId(), l.GetMeterCode(), l.GetOriginPeriodId()}] = q
	}
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
	var drafts []draftInvoice
	flush := func() error {
		if len(drafts) == 0 {
			return nil
		}
		n, err := m.insertDrafts(ctx, rr.run, drafts)
		m.invoices.Add(ctx, int64(n), metric.WithAttributes(attribute.String("environment", rr.env)))
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

// lineSet is the lines of an invoice with the display scale of each one's quantity.
type lineSet struct {
	lines  []rating.Line
	scales []int
}

func (ls *lineSet) add(l rating.Line, scale int) {
	ls.lines = append(ls.lines, l)
	ls.scales = append(ls.scales, scale)
}

// draftFor rates one customer; it returns nil when the customer gets no invoice.
func (m *Manager) draftFor(ctx context.Context, rr *rateRun, cid string) (*draftInvoice, error) {
	run, tenantCurrency := rr.run, rr.tenant.GetCurrency()
	sub, subscribed := rr.subs[cid]
	pvID, snapExternal, invoiceCurrency := "", sub.External, orDefault(sub.Currency, tenantCurrency)
	if subscribed {
		pvID = sub.PlanVersionID
	}
	ll := rr.lateLines[cid]
	if pvID == "" && len(ll) > 0 {
		pvID, snapExternal, invoiceCurrency = ll[0].planVersion, ll[0].external, orDefault(ll[0].currency, tenantCurrency)
	}
	if pvID == "" {
		m.noSub.Add(ctx, 1)
		m.log.WarnContext(ctx, "customer has usage but no subscription in the period, not invoiced", "tenant", run.TenantID, "customer", cid, "period", run.PeriodID)
		return nil, nil
	}
	var ls lineSet
	if subscribed {
		if err := m.subscriptionLines(ctx, rr, &ls, sub, cid, pvID, invoiceCurrency); err != nil {
			return nil, err
		}
	}
	for _, l := range ll {
		if orDefault(l.currency, tenantCurrency) != invoiceCurrency {
			// Money in two currencies cannot share an invoice; the late usage of an earlier subscription in another currency is not billed.
			m.log.WarnContext(ctx, "late usage in another currency than the invoice, not invoiced", "tenant", run.TenantID, "customer", cid,
				"origin", l.line.OriginPeriod, "late_currency", l.currency, "invoice_currency", invoiceCurrency)
			continue
		}
		ls.add(l.line, rr.meters[l.line.MeterCode].Scale)
	}
	if len(ls.lines) == 0 {
		return nil, nil
	}
	cu, err := m.cfg.Plans.GetCustomer(rr.tctx, &plansv1.GetCustomerRequest{ExternalId: snapExternal})
	if err != nil {
		return nil, fmt.Errorf("get customer %s: %w", snapExternal, err)
	}
	// Tax at the tenant's rate for the customer's country, on an invoice Quotify raises. What a provider charges for is taxed by the provider.
	if !subscribed || sub.Owner != "provider" {
		if name, bps, ok := rr.taxes.rateFor(cu.GetCustomer().GetCountry()); ok {
			if tl, ok := rating.TaxLine(ls.lines, name, bps); ok {
				ls.add(tl, 0)
			}
		}
	}
	return m.buildDraft(rr, cu.GetCustomer(), &ls, cid, pvID, snapExternal, invoiceCurrency, sub, subscribed)
}

// subscriptionLines adds the fixed fee and the usage of a customer's subscription to ls.
func (m *Manager) subscriptionLines(ctx context.Context, rr *rateRun, ls *lineSet, sub subscription, cid, pvID, invoiceCurrency string) error {
	tenantID, tenantCurrency := rr.run.TenantID, rr.tenant.GetCurrency()
	pv, err := m.planVersion(ctx, rr.plans, tenantID, pvID, invoiceCurrency, tenantCurrency)
	if err != nil {
		return err
	}
	segs, err := m.feeSegments(ctx, rr.plans, tenantID, sub, rr.start, rr.end, tenantCurrency)
	if err != nil {
		return err
	}
	fixed, err := rating.FixedFeeLines(segs, rr.start, rr.end)
	if err != nil {
		return permanent{err}
	}
	used, err := rating.RateUsage(pv, rr.usage[cid])
	if err != nil {
		return permanent{err}
	}
	for _, l := range append(fixed, used...) {
		if l.ServiceFrom.IsZero() { // usage covers the whole period; a fixed fee line says its own segment
			l.ServiceFrom, l.ServiceTo = rr.start, rr.end
		}
		ls.add(l, rr.meters[l.MeterCode].Scale)
	}
	return nil
}

// buildDraft totals the lines and freezes the snapshot of the invoice's addressees.
func (m *Manager) buildDraft(rr *rateRun, cu *plansv1.Customer, ls *lineSet, cid, pvID, snapExternal, invoiceCurrency string, sub subscription, subscribed bool) (*draftInvoice, error) {
	total, err := rating.Total(ls.lines)
	if err != nil {
		return nil, permanent{err}
	}
	if total < 0 {
		total = 0 // a negative total (credit-like late adjustments) is clamped; credits are Phase 11
	}
	invCur, ok := money.LookupCurrency(invoiceCurrency)
	if !ok {
		return nil, permanent{fmt.Errorf("subscription currency %q is not supported", invoiceCurrency)}
	}
	cuid, err := uuid.Parse(cid)
	if err != nil {
		return nil, permanent{err}
	}
	pv, err := uuid.Parse(pvID)
	if err != nil {
		return nil, permanent{err}
	}
	paymentMethod, provider := cu.GetPaymentMethod(), ""
	if subscribed {
		provider = sub.Provider
		if sub.Owner == "provider" && sub.ProviderRef != "" {
			// The provider holds the payment method of a subscription it runs; the charge goes to that subscription.
			paymentMethod = sub.ProviderRef
		}
	}
	t := rr.tenant
	snapshot := saga.Snapshot{
		CustomerExternalID: snapExternal, CustomerName: cu.GetName(), CustomerEmail: cu.GetEmail(),
		PaymentMethod: paymentMethod, Provider: provider, TenantName: t.GetName(), Environment: rr.env, CurrencyExponent: invCur.Exponent, ProviderMails: providerMails(provider),
		SellerAddress: addressLines(t.GetAddress()), CustomerAddress: addressLines(cu.GetAddress()), LogoObjectKey: rr.logoKey,
		BankTransfer: bankTransfer(t, provider, invoiceCurrency),
	}
	einvoiceSnapshot(&snapshot, t, cu)
	return &draftInvoice{
		Provider: provider, Country: cu.GetCountry(), Currency: invoiceCurrency, CustomerID: cuid, PlanVersion: pv, Lines: ls.lines, LineScales: ls.scales, Total: total,
		Snapshot: snapshot,
	}, nil
}

type lateLine struct {
	line        rating.Line
	planVersion string
	external    string
	currency    string
}

// lateLines prices the late usage billed in this period, incrementally on top of what the origin period (and the late
// batches already billed for it) cost, under the origin period's plan version.
func (m *Manager) lateLines(ctx context.Context, run *Run, gen int32, late map[lateKey]*apd.Decimal, current map[string]subscription,
	meters map[string]meterInfo, plans map[string]rating.PlanVersion, loc *time.Location, tenantCurrency string) (map[string][]lateLine, error) {
	out := map[string][]lateLine{}
	if len(late) == 0 {
		return out, nil
	}
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
	subsByOrigin := map[string]map[string]subscription{}
	for o := range origins {
		s, e, err := period.Bounds(o, loc)
		if err != nil {
			return nil, permanent{err}
		}
		if subsByOrigin[o], err = m.subscriptions(ctx, run.TenantID, s, e); err != nil {
			return nil, err
		}
	}

	for _, k := range keys {
		sub, ok := subsByOrigin[k.Origin][k.Customer]
		if !ok {
			sub, ok = current[k.Customer]
		}
		if !ok {
			m.noSub.Add(ctx, 1)
			m.log.WarnContext(ctx, "late usage of a customer without a subscription, not invoiced", "customer", k.Customer, "meter", k.Meter, "origin", k.Origin)
			continue
		}
		pv, err := m.planVersion(ctx, plans, run.TenantID, sub.PlanVersionID, sub.Currency, tenantCurrency)
		if err != nil {
			return nil, err
		}
		already, err := alreadyBilled(base[k], prior[k])
		if err != nil {
			return nil, err
		}
		line, has, err := rating.LateLine(pv, k.Meter, meters[k.Meter].Unit, k.Origin, already, late[k])
		if err != nil {
			return nil, permanent{err}
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
	for start := 0; start < len(keys); start += 1000 {
		chunk := keys[start:min(start+1000, len(keys))]
		req := &aggregatorv1.GetUsageBatchRequest{Generation: gen}
		for _, k := range chunk {
			req.Keys = append(req.Keys, &aggregatorv1.UsageKey{CustomerId: k.Customer, MeterCode: k.Meter, PeriodId: k.Origin})
		}
		resp, err := m.cfg.Aggregator.GetUsageBatch(tenantCtx(ctx, tenantID), req)
		if err != nil {
			return nil, fmt.Errorf("origin usage: %w", err)
		}
		for i, u := range resp.GetUsage() {
			q, err := decimal(u.GetQuantity())
			if err != nil {
				return nil, permanent{err}
			}
			base[chunk[i]] = q
		}
	}
	return base, nil
}

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
		return nil, permanent{err}
	}
	return &already, nil
}

var apdCtx = apd.BaseContext.WithPrecision(80)

// priorLate sums the LATE_USAGE lines of earlier invoices per (customer, meter, origin).
func (m *Manager) priorLate(ctx context.Context, run *Run, origins map[string]bool) (map[lateKey]*apd.Decimal, error) {
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
		d, err := decimal(r.Quantity)
		if err != nil {
			return nil, permanent{err}
		}
		var q apd.Decimal
		if _, err := apdCtx.Quo(&q, d, apd.New(1, int32(r.QuantityScale))); err != nil {
			return nil, permanent{err}
		}
		out[lateKey{r.CustomerID, deref(r.MeterCode), deref(r.OriginPeriodID)}] = &q
	}
	return out, nil
}

// unitsOf renders a decimal quantity as scaled integer units with the given scale (the stored form).
func unitsOf(q *apd.Decimal, scale int) (string, error) {
	var r apd.Decimal
	if _, err := apdCtx.Quantize(&r, q, -int32(scale)); err != nil { //nolint:gosec // scale 0..6
		return "", err
	}
	s := r.Text('f')
	if scale == 0 {
		return strings.SplitN(s, ".", 2)[0], nil
	}
	return strings.Replace(s, ".", "", 1), nil
}

func (m *Manager) insertDrafts(ctx context.Context, run *Run, drafts []draftInvoice) (int, error) {
	created := 0
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		created = 0
		q := m.q.WithTx(tx)
		for _, d := range drafts {
			inserted, err := insertDraft(ctx, q, run, d)
			if err != nil {
				return err
			}
			if inserted {
				created++
			}
		}
		return nil
	})
	return created, err
}

// insertDraft writes one draft invoice and its lines; false means the customer already has its invoice for the period.
func insertDraft(ctx context.Context, q *sqlc.Queries, run *Run, d draftInvoice) (bool, error) {
	snap, _ := json.Marshal(d.Snapshot)
	id := uuid.New()
	var subtotal int64 // before tax
	for _, l := range d.Lines {
		if l.Kind != rating.KindTax {
			subtotal += l.AmountMinor
		}
	}
	_, err := q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
		TenantID: run.TenantID, ID: id, CustomerID: d.CustomerID, PeriodID: run.PeriodID, Kind: "REGULAR", PlanVersionID: d.PlanVersion, Currency: d.Currency,
		SubtotalMinor: subtotal, TotalMinor: d.Total, Snapshot: snap, Provider: d.Provider, Country: store.NilIfEmpty(d.Country),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	lines, scales := d.Lines, d.LineScales
	// Credits from re-rating are applied to the first regular invoice that can use them.
	credit, err := applyCredits(ctx, q, run.TenantID, d.CustomerID, id, d.Currency, d.Total)
	if err != nil {
		return false, err
	}
	if credit > 0 {
		lines = append(append([]rating.Line(nil), lines...), rating.Line{Kind: rating.KindCredit, AmountMinor: -credit,
			Description: "Credit from a price correction of an earlier period"})
		scales = append(append([]int(nil), scales...), 0)
		if err := q.SetInvoiceTotals(ctx, sqlc.SetInvoiceTotalsParams{SubtotalMinor: subtotal - credit, TotalMinor: d.Total - credit, TenantID: run.TenantID, ID: id}); err != nil {
			return false, err
		}
	}
	return true, insertLines(ctx, q, run.TenantID, id, lines, scales)
}

// insertLines writes the lines of an invoice, numbered from 1; scales[i] is the scale of lines[i]'s quantity.
func insertLines(ctx context.Context, q *sqlc.Queries, tenantID, invoiceID uuid.UUID, lines []rating.Line, scales []int) error {
	for i, l := range lines {
		p := sqlc.InsertInvoiceLineParams{
			TenantID: tenantID, InvoiceID: invoiceID, LineNo: i32(i + 1), Kind: string(l.Kind), MeterCode: store.NilIfEmpty(l.MeterCode),
			OriginPeriodID: store.NilIfEmpty(l.OriginPeriod), UnitPrice: store.NilIfEmpty(l.UnitPrice), AmountMinor: l.AmountMinor, Description: l.Description,
			ServiceFrom: store.NilIfZero(l.ServiceFrom), ServiceTo: store.NilIfZero(l.ServiceTo), CouponCode: store.NilIfEmpty(l.CouponCode),
		}
		if l.Quantity != nil {
			units, err := unitsOf(l.Quantity, scales[i])
			if err != nil {
				return err
			}
			p.Quantity, p.QuantityScale, p.UnitLabel = &units, store.Ptr(int16(scales[i])), &l.UnitLabel //nolint:gosec // 0..6
		}
		if err := q.InsertInvoiceLine(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// applyCredits consumes the customer's unapplied credit balances, oldest first, up to max minor units, records the
// applications against the invoice and returns the amount applied. A credit larger than what is left to pay stays
// partly unapplied for the next invoice.
func applyCredits(ctx context.Context, q *sqlc.Queries, tenantID, customerID, invoiceID uuid.UUID, currency string, max int64) (int64, error) {
	if max <= 0 {
		return 0, nil
	}
	credits, err := q.ListUnappliedCredits(ctx, sqlc.ListUnappliedCreditsParams{TenantID: tenantID, CustomerID: customerID, Currency: currency})
	if err != nil {
		return 0, err
	}
	var applied int64
	for _, c := range credits {
		if applied >= max {
			break
		}
		use := min(c.Remaining, max-applied)
		if use <= 0 {
			continue
		}
		if err := q.InsertCreditApplication(ctx, sqlc.InsertCreditApplicationParams{TenantID: tenantID, CreditID: c.ID, InvoiceID: invoiceID, AmountMinor: use}); err != nil {
			return 0, err
		}
		if use == c.Remaining {
			if err := q.MarkCreditApplied(ctx, sqlc.MarkCreditAppliedParams{AppliedInvoiceID: invoiceID, TenantID: tenantID, ID: c.ID}); err != nil {
				return 0, err
			}
		}
		applied += use
	}
	return applied, nil
}
