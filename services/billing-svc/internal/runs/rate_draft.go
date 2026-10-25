package runs

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

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
func (m *Manager) draftFor(ctx context.Context, rr *rateRun, cid string) (*invoicecmd.RegularDraft, error) {
	run, tenantCurrency := rr.run, rr.tenant.GetCurrency()
	sub, subscribed := rr.subs[cid]
	pvID, snapExternal, invoiceCurrency := "", sub.External, inputs.OrDefault(sub.Currency, tenantCurrency)
	if subscribed {
		pvID = sub.PlanVersionID
	}
	ll := rr.lateLines[cid]
	if pvID == "" && len(ll) > 0 {
		pvID, snapExternal, invoiceCurrency = ll[0].planVersion, ll[0].external, inputs.OrDefault(ll[0].currency, tenantCurrency)
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
		if inputs.OrDefault(l.currency, tenantCurrency) != invoiceCurrency {
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
	cu, err := m.in.Plans.GetCustomer(rr.tctx, &plansv1.GetCustomerRequest{ExternalId: snapExternal})
	if err != nil {
		return nil, fmt.Errorf("get customer %s: %w", snapExternal, err)
	}
	// Tax at the tenant's rate for the customer's country, on an invoice Payrudder raises. What a provider charges for is taxed by the provider.
	if !subscribed || !sub.ProviderOwned() {
		if name, bps, ok := rr.taxes.RateFor(cu.GetCustomer().GetCountry()); ok {
			if tl, ok := rating.TaxLine(ls.lines, name, bps); ok {
				ls.add(tl, 0)
			}
		}
	}
	return m.buildDraft(rr, cu.GetCustomer(), &ls, cid, pvID, snapExternal, invoiceCurrency, sub, subscribed)
}

// subscriptionLines adds the fixed fee and the usage of a customer's subscription to ls.
func (m *Manager) subscriptionLines(ctx context.Context, rr *rateRun, ls *lineSet, sub inputs.Subscription, cid, pvID, invoiceCurrency string) error {
	tenantID, tenantCurrency := rr.run.TenantID, rr.tenant.GetCurrency()
	pv, err := m.in.PlanVersion(ctx, rr.plans, tenantID, pvID, invoiceCurrency, tenantCurrency)
	if err != nil {
		return err
	}
	segs, err := m.in.FeeSegments(ctx, rr.plans, tenantID, sub, rr.start, rr.end, tenantCurrency)
	if err != nil {
		return err
	}
	fixed, err := rating.FixedFeeLines(segs, rr.start, rr.end)
	if err != nil {
		return inputs.Permanent(err)
	}
	used, err := rating.RateUsage(pv, rr.usage[cid])
	if err != nil {
		return inputs.Permanent(err)
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
func (m *Manager) buildDraft(rr *rateRun, cu *plansv1.Customer, ls *lineSet, cid, pvID, snapExternal, invoiceCurrency string, sub inputs.Subscription, subscribed bool) (*invoicecmd.RegularDraft, error) {
	total, err := rating.Total(ls.lines)
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	if total < 0 {
		total = 0 // a negative total (credit-like late adjustments) is clamped; the lines keep their amounts
	}
	exponent, ok := inputs.CurrencyExponent(invoiceCurrency)
	if !ok {
		return nil, inputs.Permanent(fmt.Errorf("subscription currency %q is not supported", invoiceCurrency))
	}
	cuid, err := uuid.Parse(cid)
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	pv, err := uuid.Parse(pvID)
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	paymentMethod, provider := cu.GetPaymentMethod(), ""
	if subscribed {
		provider = sub.Provider
		if sub.ProviderOwned() && sub.ProviderRef != "" {
			// The provider holds the payment method of a subscription it runs; the charge goes to that subscription.
			paymentMethod = sub.ProviderRef
		}
	}
	snapshot := inputs.SnapshotOf{TenantID: rr.run.TenantID, Tenant: rr.tenant, Customer: cu, External: snapExternal, Provider: provider, PaymentMethod: paymentMethod,
		Currency: invoiceCurrency, CurrencyExponent: exponent}.Snapshot()
	return &invoicecmd.RegularDraft{
		Provider: provider, Country: cu.GetCountry(), Currency: invoiceCurrency, CustomerID: cuid, PlanVersion: pv, Lines: ls.lines, LineScales: ls.scales, Total: total,
		Snapshot: snapshot,
	}, nil
}
