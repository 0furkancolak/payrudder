package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

// Upcoming is the regular invoice a customer's period would get if it were drafted now. Nothing of it is stored.
type Upcoming struct {
	PeriodID    string
	Start, End  time.Time
	AsOf        time.Time
	Currency    string
	Lines       []rating.Line
	Scales      []int
	Subtotal    int64
	Tax, Credit int64
	Total       int64
	// Surplus is what the lines come to below zero, which the real invoice would add to the customer's credit balance.
	Surplus int64
	// Has is false when the customer would get no invoice (no subscription in the period and no late usage to bill).
	Has bool
}

// Upcoming prices the customer's next regular invoice with the code that drafts the real one (draftFor): the same subscription, plan version, usage, late usage, fee
// segments, tax and clamp, read the way a billing run reads them but for one customer, and the credit balance the invoice would use (invoicecmd.PlanCredits) without
// using it. The period is the one whose invoice has not been raised yet: the current one, or the previous one while its grace period runs. A fixed fee invoiced in
// advance is its own FEE invoice and is not here, as it is not on the regular invoice.
func (m *Manager) Upcoming(ctx context.Context, tenantID uuid.UUID, externalID string) (*Upcoming, error) {
	t, err := m.in.Tenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	now := m.Now()
	run := &model.Run{TenantID: tenantID, PeriodID: period.PeriodOf(now.Add(-t.GetGracePeriod().AsDuration()), loc)}
	rr, err := m.rateRunOf(ctx, run, t)
	if err != nil {
		return nil, err
	}
	cu, ok, err := m.in.Customer(ctx, tenantID, externalID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errkey.New(codes.NotFound, "billing.upcoming.customer_not_found", "customer not found")
	}
	cid := cu.GetId()
	out := &Upcoming{PeriodID: run.PeriodID, Start: rr.start, End: rr.end, AsOf: now, Currency: t.GetCurrency()}

	subs, err := m.in.Subscriptions(ctx, tenantID, rr.start, rr.end)
	if err != nil {
		return nil, err
	}
	if sub, ok := subs[cid]; ok {
		rr.subs = map[string]inputs.Subscription{cid: sub}
	}
	usage, err := m.in.CustomerUsage(rr.tctx, cid, run.PeriodID, 0, rr.meters)
	if err != nil {
		return nil, err
	}
	rr.usage = map[string][]rating.Usage{cid: usage}
	late, err := m.customerLateUsage(rr, cid)
	if err != nil {
		return nil, err
	}
	if rr.taxes, err = m.in.TaxRates(ctx, tenantID); err != nil {
		return nil, err
	}
	if rr.lateLines, err = m.lateLines(ctx, rr, 0, late); err != nil {
		return nil, err
	}
	if len(rr.subs) == 0 && len(rr.lateLines[cid]) == 0 {
		return out, nil // draftFor would log a customer without a subscription, which here is only a customer with nothing to bill
	}
	d, err := m.draftFor(ctx, rr, cid)
	if err != nil || d == nil {
		return out, err
	}
	cuid, err := uuid.Parse(cid)
	if err != nil {
		return nil, inputs.Permanent(err)
	}
	credit, err := invoicecmd.PreviewCredit(ctx, m.on(tenantID), tenantID, cuid, d.Currency, d.Total)
	if err != nil {
		return nil, err
	}
	out.Has, out.Currency, out.Credit, out.Total, out.Surplus = true, d.Currency, credit, d.Total-credit, d.CreditSurplus
	out.Subtotal = rating.Subtotal(d.Lines) - credit
	for _, l := range d.Lines {
		if l.Kind == rating.KindTax {
			out.Tax += l.AmountMinor
		}
	}
	out.Lines, out.Scales = d.Lines, d.LineScales
	if credit > 0 {
		out.Lines, out.Scales = invoicecmd.WithCreditLine(d.Lines, d.LineScales, credit, invoicecmd.RegularCreditDescription)
	}
	return out, nil
}

// customerLateUsage is the late usage billed in the preview's period that belongs to one customer. The aggregator streams a period's late usage for all customers, as a
// billing run reads it; it is a handful of rows per period, so the filter is here.
func (m *Manager) customerLateUsage(rr *rateRun, customerID string) (map[lateKey]*apd.Decimal, error) {
	ls, err := m.in.Aggregator.StreamLateUsage(rr.tctx, &aggregatorv1.StreamLateUsageRequest{BilledPeriodId: rr.run.PeriodID})
	if err != nil {
		return nil, fmt.Errorf("stream late usage: %w", err)
	}
	late := map[lateKey]*apd.Decimal{}
	for {
		r, err := ls.Recv()
		if errors.Is(err, io.EOF) {
			return late, nil
		}
		if err != nil {
			return nil, fmt.Errorf("stream late usage: %w", err)
		}
		l := r.GetLateUsage()
		if l.GetCustomerId() != customerID {
			continue
		}
		q, err := inputs.Decimal(l.GetQuantity())
		if err != nil {
			return nil, inputs.Permanent(err)
		}
		late[lateKey{l.GetCustomerId(), l.GetMeterCode(), l.GetOriginPeriodId()}] = q
	}
}
