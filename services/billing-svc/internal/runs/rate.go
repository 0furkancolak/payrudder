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
)

type meterInfo struct {
	Unit  string
	Scale int
}

type subscription struct {
	CustomerID, External string
	PlanVersionID        string
	Start                time.Time
}

// subscriptions returns, per customer, the subscription that was active last in the period (a plan change inside a
// period is billed at the later plan; there is no proration in v1).
func (m *Manager) subscriptions(ctx context.Context, tenantID uuid.UUID, start, end time.Time) (map[string]subscription, error) {
	stream, err := m.cfg.Plans.ListSubscriptionsForPeriod(tenantCtx(ctx, tenantID), &plansv1.ListSubscriptionsForPeriodRequest{
		PeriodStart: timestampOf(start), PeriodEnd: timestampOf(end)})
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	out := map[string]subscription{}
	for {
		r, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("list subscriptions: %w", err)
		}
		s := r.GetSubscription()
		cur, ok := out[s.GetCustomerId()]
		if !ok || s.GetStartTime().AsTime().After(cur.Start) {
			out[s.GetCustomerId()] = subscription{CustomerID: s.GetCustomerId(), External: s.GetCustomerExternalId(),
				PlanVersionID: s.GetPlanVersionId(), Start: s.GetStartTime().AsTime()}
		}
	}
}

func (m *Manager) planVersion(ctx context.Context, cache map[string]rating.PlanVersion, tenantID uuid.UUID, id string) (rating.PlanVersion, error) {
	if pv, ok := cache[id]; ok {
		return pv, nil
	}
	resp, err := m.cfg.Plans.GetPlanVersion(tenantCtx(ctx, tenantID), &plansv1.GetPlanVersionRequest{PlanVersionId: id})
	if err != nil {
		return rating.PlanVersion{}, fmt.Errorf("get plan version %s: %w", id, err)
	}
	pv, err := convertPlanVersion(resp.GetPlanVersion())
	if err != nil {
		return rating.PlanVersion{}, permanent{fmt.Errorf("plan version %s: %w", id, err)}
	}
	cache[id] = pv
	return pv, nil
}

func decimal(s string) (*apd.Decimal, error) {
	d, _, err := apd.NewFromString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return d, nil
}

func convertPlanVersion(p *plansv1.PlanVersion) (rating.PlanVersion, error) {
	pv := rating.PlanVersion{ID: p.GetId(), BaseFeeMinor: p.GetBaseFeeMinor(), Prices: map[string]rating.Price{}}
	for _, pr := range p.GetPrices() {
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

type lateKey struct{ Customer, Meter, Origin string }

// draftInvoice is an invoice about to be inserted.
type draftInvoice struct {
	CustomerID  uuid.UUID
	PlanVersion string
	Lines       []rating.Line
	LineScales  []int
	Total       int64
	Snapshot    saga.Snapshot
}

// rate gathers the period's usage, late usage and subscriptions and creates a draft invoice per customer. It is
// idempotent: drafts that exist are kept (the partial unique index one_regular_invoice), so a retried run converges.
func (m *Manager) rate(ctx context.Context, run *Run) error {
	tctx := tenantCtx(ctx, run.TenantID)
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: run.TenantID.String()})
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	t := tn.GetTenant()
	cur, ok := money.LookupCurrency(t.GetCurrency())
	if !ok {
		return permanent{fmt.Errorf("tenant currency %q is not supported", t.GetCurrency())}
	}
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return permanent{err}
	}
	start, end, err := period.Bounds(run.PeriodID, loc)
	if err != nil {
		return permanent{err}
	}
	env := "live"
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
		env = "sandbox"
	}

	metersResp, err := m.cfg.Plans.ListMeters(tctx, &plansv1.ListMetersRequest{})
	if err != nil {
		return fmt.Errorf("list meters: %w", err)
	}
	meters := map[string]meterInfo{}
	for _, mt := range metersResp.GetMeters() {
		meters[mt.GetCode()] = meterInfo{Unit: mt.GetUnitLabel(), Scale: int(mt.GetQuantityScale())}
	}

	subs, err := m.subscriptions(ctx, run.TenantID, start, end)
	if err != nil {
		return err
	}

	// On-time usage of the period, at the generation the run pins.
	gen := int32(0)
	if run.Generation != nil {
		gen = *run.Generation
	}
	usage := map[string][]rating.Usage{}
	us, err := m.cfg.Aggregator.StreamPeriodUsage(tctx, &aggregatorv1.StreamPeriodUsageRequest{PeriodId: run.PeriodID, Generation: gen})
	if err != nil {
		return fmt.Errorf("stream usage: %w", err)
	}
	for {
		r, err := us.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream usage: %w", err)
		}
		u := r.GetUsage()
		if gen == 0 {
			gen = u.GetGeneration()
		}
		q, err := decimal(u.GetQuantity())
		if err != nil {
			return permanent{err}
		}
		usage[u.GetCustomerId()] = append(usage[u.GetCustomerId()], rating.Usage{MeterCode: u.GetMeterCode(), Quantity: q, UnitLabel: meters[u.GetMeterCode()].Unit})
	}
	if gen == 0 {
		gen = 1 // no usage rows: the tenant has only ever had generation 1
	}
	if run.Generation == nil {
		if _, err := m.cfg.Pool.Exec(ctx, `UPDATE billing_runs SET generation = $3 WHERE tenant_id = $1 AND period_id = $2 AND generation IS NULL`,
			run.TenantID, run.PeriodID, gen); err != nil {
			return err
		}
		run.Generation = &gen
	}

	// Late usage billed in this period.
	late := map[lateKey]*apd.Decimal{}
	ls, err := m.cfg.Aggregator.StreamLateUsage(tctx, &aggregatorv1.StreamLateUsageRequest{BilledPeriodId: run.PeriodID, Generation: gen})
	if err != nil {
		return fmt.Errorf("stream late usage: %w", err)
	}
	for {
		r, err := ls.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream late usage: %w", err)
		}
		l := r.GetLateUsage()
		q, err := decimal(l.GetQuantity())
		if err != nil {
			return permanent{err}
		}
		late[lateKey{l.GetCustomerId(), l.GetMeterCode(), l.GetOriginPeriodId()}] = q
	}

	customers := map[string]struct{}{}
	for c := range subs {
		customers[c] = struct{}{}
	}
	for c := range usage {
		customers[c] = struct{}{}
	}
	for k := range late {
		customers[k.Customer] = struct{}{}
	}

	plans := map[string]rating.PlanVersion{}
	lateLines, err := m.lateLines(ctx, run, gen, late, subs, meters, plans, loc)
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(customers))
	for c := range customers {
		ids = append(ids, c)
	}
	sort.Strings(ids)

	var drafts []draftInvoice
	flush := func() error {
		if len(drafts) == 0 {
			return nil
		}
		n, err := m.insertDrafts(ctx, run, t.GetCurrency(), drafts)
		m.invoices.Add(ctx, int64(n), metric.WithAttributes(attribute.String("environment", env)))
		drafts = drafts[:0]
		return err
	}
	for _, cid := range ids {
		sub, subscribed := subs[cid]
		var pvID string
		if subscribed {
			pvID = sub.PlanVersionID
		}
		var lines []rating.Line
		var scales []int
		var snapExternal string
		snapExternal = sub.External
		ll := lateLines[cid]
		if pvID == "" && len(ll) > 0 {
			pvID, snapExternal = ll[0].planVersion, ll[0].external
		}
		if pvID == "" {
			m.noSub.Add(ctx, 1)
			m.log.WarnContext(ctx, "customer has usage but no subscription in the period, not invoiced", "tenant", run.TenantID, "customer", cid, "period", run.PeriodID)
			continue
		}
		if subscribed {
			pv, err := m.planVersion(ctx, plans, run.TenantID, pvID)
			if err != nil {
				return err
			}
			rated, err := rating.Rate(pv, usage[cid])
			if err != nil {
				return permanent{err}
			}
			for _, l := range rated {
				lines = append(lines, l)
				scales = append(scales, meters[l.MeterCode].Scale)
			}
		}
		for _, l := range ll {
			lines = append(lines, l.line)
			scales = append(scales, meters[l.line.MeterCode].Scale)
		}
		if len(lines) == 0 {
			continue
		}
		total, err := rating.Total(lines)
		if err != nil {
			return permanent{err}
		}
		if total < 0 {
			total = 0 // a negative total (credit-like late adjustments) is clamped; credits are Phase 11
		}
		cu, err := m.cfg.Plans.GetCustomer(tctx, &plansv1.GetCustomerRequest{ExternalId: snapExternal})
		if err != nil {
			return fmt.Errorf("get customer %s: %w", snapExternal, err)
		}
		cuid, err := uuid.Parse(cid)
		if err != nil {
			return permanent{err}
		}
		drafts = append(drafts, draftInvoice{
			CustomerID: cuid, PlanVersion: pvID, Lines: lines, LineScales: scales, Total: total,
			Snapshot: saga.Snapshot{
				CustomerExternalID: snapExternal, CustomerName: cu.GetCustomer().GetName(), CustomerEmail: cu.GetCustomer().GetEmail(),
				PaymentMethod: cu.GetCustomer().GetPaymentMethod(), TenantName: t.GetName(), Environment: env, CurrencyExponent: cur.Exponent,
			},
		})
		if len(drafts) >= m.cfg.PageSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}

	if _, err := m.cfg.Pool.Exec(ctx, `UPDATE billing_runs SET invoice_count = (SELECT count(*) FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND kind = 'REGULAR')
		WHERE tenant_id = $1 AND period_id = $2`, run.TenantID, run.PeriodID); err != nil {
		return err
	}
	// The aggregates of the period are final: later on-time-looking arrivals are routed to late usage.
	if _, err := m.cfg.Aggregator.FreezePeriod(tctx, &aggregatorv1.FreezePeriodRequest{PeriodId: run.PeriodID}); err != nil {
		return fmt.Errorf("freeze period: %w", err)
	}
	return nil
}

type lateLine struct {
	line        rating.Line
	planVersion string
	external    string
}

// lateLines prices the late usage billed in this period, incrementally on top of what the origin period (and the late
// batches already billed for it) cost, under the origin period's plan version.
func (m *Manager) lateLines(ctx context.Context, run *Run, gen int32, late map[lateKey]*apd.Decimal, current map[string]subscription,
	meters map[string]meterInfo, plans map[string]rating.PlanVersion, loc *time.Location) (map[string][]lateLine, error) {
	out := map[string][]lateLine{}
	if len(late) == 0 {
		return out, nil
	}
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

	// The on-time quantity of each origin period.
	base := map[lateKey]*apd.Decimal{}
	for start := 0; start < len(keys); start += 1000 {
		chunk := keys[start:min(start+1000, len(keys))]
		req := &aggregatorv1.GetUsageBatchRequest{Generation: gen}
		for _, k := range chunk {
			req.Keys = append(req.Keys, &aggregatorv1.UsageKey{CustomerId: k.Customer, MeterCode: k.Meter, PeriodId: k.Origin})
		}
		resp, err := m.cfg.Aggregator.GetUsageBatch(tenantCtx(ctx, run.TenantID), req)
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
	// Late usage of the same origin that earlier invoices already billed.
	prior, err := m.priorLate(ctx, run, origins, meters)
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
		pv, err := m.planVersion(ctx, plans, run.TenantID, sub.PlanVersionID)
		if err != nil {
			return nil, err
		}
		b := base[k]
		if b == nil {
			b = apd.New(0, 0)
		}
		var already apd.Decimal
		if p := prior[k]; p != nil {
			if _, err := apdCtx.Add(&already, b, p); err != nil {
				return nil, permanent{err}
			}
		} else {
			already.Set(b)
		}
		line, has, err := rating.LateLine(pv, k.Meter, meters[k.Meter].Unit, k.Origin, &already, late[k])
		if err != nil {
			return nil, permanent{err}
		}
		if has {
			out[k.Customer] = append(out[k.Customer], lateLine{line: line, planVersion: sub.PlanVersionID, external: sub.External})
		}
	}
	return out, nil
}

var apdCtx = apd.BaseContext.WithPrecision(80)

// priorLate sums the LATE_USAGE lines of earlier invoices per (customer, meter, origin).
func (m *Manager) priorLate(ctx context.Context, run *Run, origins map[string]bool, meters map[string]meterInfo) (map[lateKey]*apd.Decimal, error) {
	list := make([]string, 0, len(origins))
	for o := range origins {
		list = append(list, o)
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT i.customer_id::text, l.meter_code, l.origin_period_id, sum(l.quantity)::text, max(l.quantity_scale)
		FROM invoice_lines l JOIN invoices i ON i.tenant_id = l.tenant_id AND i.id = l.invoice_id
		WHERE l.tenant_id = $1 AND l.kind = 'LATE_USAGE' AND l.origin_period_id = ANY($2) AND i.period_id <> $3 AND i.status <> 'void'
		GROUP BY 1, 2, 3`, run.TenantID, list, run.PeriodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[lateKey]*apd.Decimal{}
	for rows.Next() {
		var c, mc, o, sum string
		var scale int16
		if err := rows.Scan(&c, &mc, &o, &sum, &scale); err != nil {
			return nil, err
		}
		d, err := decimal(sum)
		if err != nil {
			return nil, permanent{err}
		}
		var q apd.Decimal
		if _, err := apdCtx.Quo(&q, d, apd.New(1, int32(scale))); err != nil { //nolint:gosec // scale 0..6
			return nil, permanent{err}
		}
		out[lateKey{c, mc, o}] = &q
	}
	return out, rows.Err()
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

func (m *Manager) insertDrafts(ctx context.Context, run *Run, currency string, drafts []draftInvoice) (int, error) {
	created := 0
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		created = 0
		for _, d := range drafts {
			snap, _ := json.Marshal(d.Snapshot)
			id := uuid.New()
			var subtotal int64
			for _, l := range d.Lines {
				subtotal += l.AmountMinor
			}
			var got uuid.UUID
			err := tx.QueryRow(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot)
				VALUES ($1,$2,$3,$4,'REGULAR',$5,$6,$7,$8,'draft',$9) ON CONFLICT DO NOTHING RETURNING id`,
				run.TenantID, id, d.CustomerID, run.PeriodID, d.PlanVersion, currency, subtotal, d.Total, snap).Scan(&got)
			if errors.Is(err, pgx.ErrNoRows) {
				continue // this customer already has its invoice for the period
			}
			if err != nil {
				return err
			}
			created++
			for i, l := range d.Lines {
				var qty, scale, label, price any
				if l.Quantity != nil {
					units, err := unitsOf(l.Quantity, d.LineScales[i])
					if err != nil {
						return err
					}
					qty, scale, label = units, int16(d.LineScales[i]), l.UnitLabel //nolint:gosec // 0..6
				}
				if l.UnitPrice != "" {
					price = l.UnitPrice
				}
				var meter, origin any
				if l.MeterCode != "" {
					meter = l.MeterCode
				}
				if l.OriginPeriod != "" {
					origin = l.OriginPeriod
				}
				if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, meter_code, origin_period_id, quantity, quantity_scale, unit_label, unit_price, amount_minor, description)
					VALUES ($1,$2,$3,$4,$5,$6,$7::numeric,$8,$9,$10::numeric,$11,$12)`,
					run.TenantID, id, i+1, string(l.Kind), meter, origin, qty, scale, label, price, l.AmountMinor, l.Description); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return created, err
}
