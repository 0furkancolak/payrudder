package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	invmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// invoiceSpec describes an invoice that is not part of a billing run: one fee period, or what a plan change moved.
type invoiceSpec struct {
	// Key makes the invoice findable again: a second pass for the same fee period or adjustment gets the invoice the first one made.
	Key         string
	Sub         *plansv1.Subscription
	PeriodStart time.Time
	Lines       []rating.Line
}

// draft is a FEE invoice about to be inserted.
type draft struct {
	id, customerID, planVersionID uuid.UUID
	key, periodID, currency       string
	provider                      string
	country                       *string
	total, subtotal               int64
	snapshot                      []byte
	lines                         []rating.Line
}

// draftInvoice creates the draft FEE invoice for spec, or returns the one a previous pass created. The lines must add up to more than zero.
func (c *Commands) draftInvoice(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, spec invoiceSpec) (uuid.UUID, error) {
	byKey := sqlc.GetInvoiceIDByFeeKeyParams{TenantID: tenantID, FeeKey: spec.Key}
	existing, err := c.q.GetInvoiceIDByFeeKey(ctx, byKey)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	d, err := c.buildDraft(ctx, tenantID, t, loc, spec)
	if err != nil {
		return uuid.Nil, err
	}
	if err := c.insertDraft(ctx, tenantID, d); err != nil {
		return uuid.Nil, err
	}
	if existing, err = c.q.GetInvoiceIDByFeeKey(ctx, byKey); err != nil {
		return uuid.Nil, err
	}
	c.cfg.Invoices.Drafted(ctx, 1)
	return existing, nil
}

func (c *Commands) buildDraft(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, spec invoiceSpec) (*draft, error) {
	sub := spec.Sub
	currency := inputs.OrDefault(sub.GetCurrency(), t.GetCurrency())
	cur, ok := money.LookupCurrency(currency)
	if !ok {
		return nil, inputs.Permanent(fmt.Errorf("subscription currency %q is not supported", currency))
	}
	cu, err := c.cfg.Inputs.Plans.GetCustomer(inputs.TenantCtx(ctx, tenantID), &plansv1.GetCustomerRequest{ExternalId: sub.GetCustomerExternalId()})
	if err != nil {
		return nil, fmt.Errorf("get customer: %w", err)
	}
	cuid, err := uuid.Parse(sub.GetCustomerId())
	if err != nil {
		return nil, err
	}
	pv, err := uuid.Parse(sub.GetPlanVersionId())
	if err != nil {
		return nil, err
	}
	lines, err := c.withTax(ctx, tenantID, sub, cu.GetCustomer(), spec.Lines)
	if err != nil {
		return nil, err
	}
	total, err := rating.Total(lines)
	if err != nil {
		return nil, inputs.Permanent(fmt.Errorf("invoice %s: %w", spec.Key, err))
	}
	if total <= 0 {
		return nil, inputs.Permanent(fmt.Errorf("invoice %s adds up to %d", spec.Key, total))
	}
	snap, err := json.Marshal(inputs.SnapshotOf{TenantID: tenantID, Tenant: t, Customer: cu.GetCustomer(), External: sub.GetCustomerExternalId(), Provider: sub.GetProvider(),
		PaymentMethod: cu.GetCustomer().GetPaymentMethod(), Currency: currency, CurrencyExponent: cur.Exponent}.Snapshot())
	if err != nil {
		return nil, err
	}
	return &draft{id: uuid.New(), customerID: cuid, planVersionID: pv, key: spec.Key, periodID: period.PeriodOf(spec.PeriodStart, loc), currency: currency,
		provider: sub.GetProvider(), country: store.NilIfEmpty(cu.GetCustomer().GetCountry()), total: total, subtotal: rating.Subtotal(lines), snapshot: snap, lines: lines}, nil
}

// taxRate is the tenant's rate for the customer's country; ok is false when there is none, and for what a provider charges for (the provider taxes that).
func (c *Commands) taxRate(ctx context.Context, tenantID uuid.UUID, sub *plansv1.Subscription, cu *plansv1.Customer) (name string, bps int32, ok bool, err error) {
	if inputs.OwnedByProvider(sub.GetOwner()) {
		return "", 0, false, nil
	}
	taxes, err := c.cfg.Inputs.TaxRates(ctx, tenantID)
	if err != nil {
		return "", 0, false, err
	}
	name, bps, ok = taxes.RateFor(cu.GetCountry())
	return name, bps, ok, nil
}

// withTax adds the tax line at the tenant's rate for the customer's country.
func (c *Commands) withTax(ctx context.Context, tenantID uuid.UUID, sub *plansv1.Subscription, cu *plansv1.Customer, lines []rating.Line) ([]rating.Line, error) {
	name, bps, ok, err := c.taxRate(ctx, tenantID, sub, cu)
	if err != nil || !ok {
		return lines, err
	}
	if tl, ok := rating.TaxLine(lines, name, bps); ok {
		return append(append([]rating.Line(nil), lines...), tl), nil
	}
	return lines, nil
}

func (c *Commands) insertDraft(ctx context.Context, tenantID uuid.UUID, d *draft) error {
	return pgxx.InTx(ctx, c.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := c.q.WithTx(tx)
		_, err := q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
			TenantID: tenantID, ID: d.id, CustomerID: d.customerID, PeriodID: d.periodID, Kind: invmodel.KindFee, PlanVersionID: d.planVersionID, Currency: d.currency,
			SubtotalMinor: d.subtotal, TotalMinor: d.total, Snapshot: d.snapshot, Provider: d.provider, Country: d.country, FeeKey: &d.key,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // another pass won the race; the lookup finds its invoice
		}
		if err != nil {
			return err
		}
		lines, scales, err := invoicecmd.ApplyCredits(ctx, q, tenantID, d.customerID, d.id, d.currency, d.subtotal, d.total, d.lines, make([]int, len(d.lines)), "Credit balance")
		if err != nil {
			return err
		}
		return invoicecmd.InsertLines(ctx, q, tenantID, d.id, lines, scales)
	})
}
