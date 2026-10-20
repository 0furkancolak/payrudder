package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rating"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
)

// BillFees invoices the fixed fees that are due in advance, for every tenant: for each advance-billed subscription whose next period has
// started, one invoice for that period's fee (kind FEE), finalized so its saga charges it, and then the subscription's next fee period is
// advanced. Every step is idempotent (the invoice is found again by its fee_key, finalizing a finalized invoice and advancing an advanced
// period do nothing), so a crash at any point is repaired by the next pass. A paused subscription's period is skipped without an invoice.
func (m *Manager) BillFees(ctx context.Context) error {
	token := ""
	var firstErr error
	for {
		resp, err := m.cfg.Admin.ListTenants(operatorCtx(ctx), &plansv1.ListTenantsRequest{PageSize: 500, PageToken: token})
		if err != nil {
			return err
		}
		for _, t := range resp.GetTenants() {
			id, err := uuid.Parse(t.GetId())
			if err != nil {
				continue
			}
			if err := m.billTenantFees(ctx, id); err != nil {
				m.log.WarnContext(ctx, "scheduler: fee billing failed", "tenant", id, "error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if resp.GetNextPageToken() == "" {
			return firstErr
		}
		token = resp.GetNextPageToken()
	}
}

func (m *Manager) billTenantFees(ctx context.Context, tenantID uuid.UUID) error {
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return fmt.Errorf("get tenant: %w", err)
	}
	t := tn.GetTenant()
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return err
	}
	tctx := tenantCtx(ctx, tenantID)
	// Changes that were waiting for the end of a period come first: the new plan's first fee is due in the same pass.
	if _, err := m.cfg.Plans.ApplyScheduledChanges(tctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(m.cfg.Now())}); err != nil {
		return fmt.Errorf("apply scheduled changes: %w", err)
	}
	if err := m.billAdjustments(ctx, tenantID, t, loc); err != nil {
		return err
	}
	after := ""
	for {
		page, err := m.cfg.Plans.ListSubscriptionsFeeDue(tctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(m.cfg.Now()), After: after, PageSize: int32(m.cfg.PageSize)}) //nolint:gosec // small
		if err != nil {
			return fmt.Errorf("list fees due: %w", err)
		}
		if len(page.GetDue()) == 0 {
			return nil
		}
		for _, d := range page.GetDue() {
			if err := m.billFee(ctx, tenantID, t, loc, d); err != nil {
				// One subscription's problem must not hold back the others; it is retried on the next pass.
				m.log.WarnContext(ctx, "fee not billed", "tenant", tenantID, "subscription", d.GetSubscription().GetId(), "error", err)
			}
		}
		after = page.GetDue()[len(page.GetDue())-1].GetSubscription().GetId()
		if len(page.GetDue()) < m.cfg.PageSize {
			return nil
		}
	}
}

func (m *Manager) billFee(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, d *plansv1.FeeDue) error {
	sub := d.GetSubscription()
	start := d.GetPeriodStart().AsTime()
	if sub.GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED && d.GetFeeMinor() > 0 {
		end := d.GetPeriodEnd().AsTime()
		lines := []rating.Line{{Kind: rating.KindBaseFee, AmountMinor: d.GetFeeMinor(), ServiceFrom: start, ServiceTo: end,
			Description: fmt.Sprintf("Base fee (%s) — %s — %s to %s", feeWord(sub), sub.GetPlanCode(), day(start, loc), day(end, loc))}}
		if d.GetDiscountMinor() > 0 {
			lines = append(lines, rating.Line{Kind: rating.KindDiscount, AmountMinor: -d.GetDiscountMinor(), ServiceFrom: start, ServiceTo: end, Description: "Coupon " + d.GetCouponCode()})
		}
		id, err := m.draftFeeInvoice(ctx, tenantID, t, loc, feeInvoiceSpec{
			Key: sub.GetId() + "|" + start.UTC().Format(time.RFC3339), Sub: sub, PeriodStart: start, Lines: lines})
		if err != nil {
			return err
		}
		if err := m.Finalize(ctx, tenantID, id); err != nil {
			return err
		}
	}
	_, err := m.cfg.Plans.AdvanceSubscriptionFee(tenantCtx(ctx, tenantID), &plansv1.AdvanceSubscriptionFeeRequest{
		SubscriptionId: sub.GetId(), PeriodStart: timestamppb.New(start)})
	return err
}

// feeInvoiceSpec describes an invoice that is not part of a billing run: one fee period, or what a plan change moved.
type feeInvoiceSpec struct {
	// Key makes the invoice findable again: a second pass for the same fee period or adjustment gets the invoice the first one made.
	Key         string
	Sub         *plansv1.Subscription
	PeriodStart time.Time
	Lines       []rating.Line
}

// draftFeeInvoice creates the draft FEE invoice for spec, or returns the one a previous pass created. The lines must add up to more than zero.
func (m *Manager) draftFeeInvoice(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, spec feeInvoiceSpec) (uuid.UUID, error) {
	sub := spec.Sub
	var existing uuid.UUID
	err := m.cfg.Pool.QueryRow(ctx, `SELECT id FROM invoices WHERE tenant_id = $1 AND fee_key = $2`, tenantID, spec.Key).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	currency := orDefault(sub.GetCurrency(), t.GetCurrency())
	cur, ok := money.LookupCurrency(currency)
	if !ok {
		return uuid.Nil, permanent{fmt.Errorf("subscription currency %q is not supported", currency)}
	}
	cu, err := m.cfg.Plans.GetCustomer(tenantCtx(ctx, tenantID), &plansv1.GetCustomerRequest{ExternalId: sub.GetCustomerExternalId()})
	if err != nil {
		return uuid.Nil, fmt.Errorf("get customer: %w", err)
	}
	cuid, err := uuid.Parse(sub.GetCustomerId())
	if err != nil {
		return uuid.Nil, err
	}
	// Tax at the tenant's rate for the customer's country (not for what a provider charges for: the provider taxes that).
	if sub.GetOwner() != "provider" {
		taxes, err := m.taxRates(ctx, tenantID)
		if err != nil {
			return uuid.Nil, err
		}
		if name, bps, ok := taxes.rateFor(cu.GetCustomer().GetCountry()); ok {
			if tl, ok := rating.TaxLine(spec.Lines, name, bps); ok {
				spec.Lines = append(append([]rating.Line(nil), spec.Lines...), tl)
			}
		}
	}
	total, err := rating.Total(spec.Lines)
	if err != nil {
		return uuid.Nil, permanent{fmt.Errorf("invoice %s: %w", spec.Key, err)}
	}
	if total <= 0 {
		return uuid.Nil, permanent{fmt.Errorf("invoice %s adds up to %d", spec.Key, total)}
	}
	var tax int64
	for _, l := range spec.Lines {
		if l.Kind == rating.KindTax {
			tax += l.AmountMinor
		}
	}
	env := "live"
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
		env = "sandbox"
	}
	logoKey := ""
	if id := t.GetInvoiceLogoAssetId(); id != "" {
		logoKey = tenantID.String() + "/assets/" + id
	}
	snap, err := json.Marshal(saga.Snapshot{
		CustomerExternalID: sub.GetCustomerExternalId(), CustomerName: cu.GetCustomer().GetName(), CustomerEmail: cu.GetCustomer().GetEmail(),
		PaymentMethod: cu.GetCustomer().GetPaymentMethod(), Provider: sub.GetProvider(), TenantName: t.GetName(), Environment: env, CurrencyExponent: cur.Exponent, LogoObjectKey: logoKey, ProviderMails: providerMails(sub.GetProvider()),
	})
	if err != nil {
		return uuid.Nil, err
	}
	var country any
	if c := cu.GetCustomer().GetCountry(); c != "" {
		country = c
	}
	id := uuid.New()
	err = pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var got uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO invoices (tenant_id, id, customer_id, period_id, kind, plan_version_id, currency, subtotal_minor, total_minor, status, snapshot, provider, country, fee_key)
			VALUES ($1,$2,$3,$4,'FEE',$5,$6,$7,$12,'draft',$8,$9,$10,$11) ON CONFLICT DO NOTHING RETURNING id`,
			tenantID, id, cuid, period.PeriodOf(spec.PeriodStart, loc), sub.GetPlanVersionId(), currency, total-tax, snap, sub.GetProvider(), country, spec.Key, total).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // another pass won the race; the lookup below finds its invoice
		}
		if err != nil {
			return err
		}
		credit, err := applyCredits(ctx, tx, tenantID, cuid, id, currency, total)
		if err != nil {
			return err
		}
		lines := spec.Lines
		if credit > 0 {
			lines = append(append([]rating.Line(nil), lines...), rating.Line{Kind: rating.KindCredit, AmountMinor: -credit, Description: "Credit balance"})
			if _, err := tx.Exec(ctx, `UPDATE invoices SET subtotal_minor = $3, total_minor = $4 WHERE tenant_id = $1 AND id = $2`, tenantID, id, total-tax-credit, total-credit); err != nil {
				return err
			}
		}
		for i, l := range lines {
			if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, amount_minor, description, service_from, service_to) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				tenantID, id, i+1, string(l.Kind), l.AmountMinor, l.Description, nullTime(l.ServiceFrom), nullTime(l.ServiceTo)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT id FROM invoices WHERE tenant_id = $1 AND fee_key = $2`, tenantID, spec.Key).Scan(&existing); err != nil {
		return uuid.Nil, err
	}
	m.invoices.Add(ctx, 1)
	return existing, nil
}

func feeWord(sub *plansv1.Subscription) string {
	if sub.GetInterval() == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
		return "yearly"
	}
	return "monthly"
}

func day(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

// billAdjustments turns what plan changes moved into money: an invoice when the new plan costs more than the unused old time was worth,
// a credit balance when it costs less. Each adjustment is handled once (its invoice or credit is found again by key) and then marked.
func (m *Manager) billAdjustments(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location) error {
	tctx := tenantCtx(ctx, tenantID)
	resp, err := m.cfg.Plans.ListSubscriptionAdjustments(tctx, &plansv1.ListSubscriptionAdjustmentsRequest{PageSize: int32(m.cfg.PageSize)}) //nolint:gosec // small
	if err != nil {
		return fmt.Errorf("list adjustments: %w", err)
	}
	for _, a := range resp.GetAdjustments() {
		if err := m.billAdjustment(ctx, tenantID, t, loc, a); err != nil {
			m.log.WarnContext(ctx, "adjustment not billed", "tenant", tenantID, "adjustment", a.GetId(), "error", err)
		}
	}
	return nil
}

func (m *Manager) billAdjustment(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, a *plansv1.Adjustment) error {
	sub := a.GetSubscription()
	net := a.GetChargeMinor() - a.GetCreditMinor()
	switch {
	case net > 0:
		from, to := a.GetFromTime().AsTime(), a.GetToTime().AsTime()
		lines := []rating.Line{{Kind: rating.KindBaseFee, AmountMinor: a.GetChargeMinor(), ServiceFrom: from, ServiceTo: to,
			Description: fmt.Sprintf("Base fee (%s) — %s — %s to %s", feeWord(sub), sub.GetPlanCode(), day(from, loc), day(to, loc))}}
		if a.GetCreditMinor() > 0 {
			lines = append(lines, rating.Line{Kind: rating.KindCredit, AmountMinor: -a.GetCreditMinor(), ServiceFrom: from, ServiceTo: to,
				Description: fmt.Sprintf("Unused time on %s — %s to %s", a.GetOldPlanCode(), day(from, loc), day(to, loc))})
		}
		id, err := m.draftFeeInvoice(ctx, tenantID, t, loc, feeInvoiceSpec{Key: "adj|" + a.GetId(), Sub: sub, PeriodStart: from, Lines: lines})
		if err != nil {
			return err
		}
		if err := m.Finalize(ctx, tenantID, id); err != nil {
			return err
		}
	case net < 0:
		cuid, err := uuid.Parse(sub.GetCustomerId())
		if err != nil {
			return err
		}
		if _, err := m.cfg.Pool.Exec(ctx, `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_adjustment_id, currency)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (tenant_id, source_adjustment_id) WHERE source_adjustment_id IS NOT NULL DO NOTHING`,
			tenantID, cuid, uuid.New(), -net, a.GetId(), orDefault(sub.GetCurrency(), t.GetCurrency())); err != nil {
			return err
		}
	}
	_, err := m.cfg.Plans.MarkSubscriptionAdjustmentBilled(tenantCtx(ctx, tenantID), &plansv1.MarkSubscriptionAdjustmentBilledRequest{AdjustmentId: a.GetId()})
	return err
}

// taxTable is a tenant's tax rates by customer country.
type taxTable map[string]*plansv1.TaxRate

func (m *Manager) taxRates(ctx context.Context, tenantID uuid.UUID) (taxTable, error) {
	resp, err := m.cfg.Plans.GetTaxRates(tenantCtx(ctx, tenantID), &plansv1.GetTaxRatesRequest{})
	if err != nil {
		return nil, fmt.Errorf("get tax rates: %w", err)
	}
	t := taxTable{}
	for _, r := range resp.GetRates() {
		t[r.GetCountry()] = r
	}
	return t, nil
}

// rateFor is the rate for a customer's country: its own, else the tenant's rate for every other country, else none.
func (t taxTable) rateFor(country string) (string, int32, bool) {
	if r, ok := t[country]; ok && country != "" {
		return r.GetName(), r.GetRateBps(), true
	}
	if r, ok := t[""]; ok {
		return r.GetName(), r.GetRateBps(), true
	}
	return "", 0, false
}
