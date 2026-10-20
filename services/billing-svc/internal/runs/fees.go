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
		id, err := m.draftFeeInvoice(ctx, tenantID, t, loc, d)
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

// draftFeeInvoice creates the draft FEE invoice of a fee period, or returns the one a previous pass created.
func (m *Manager) draftFeeInvoice(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, d *plansv1.FeeDue) (uuid.UUID, error) {
	sub := d.GetSubscription()
	start, end := d.GetPeriodStart().AsTime(), d.GetPeriodEnd().AsTime()
	key := sub.GetId() + "|" + start.UTC().Format(time.RFC3339)
	var existing uuid.UUID
	err := m.cfg.Pool.QueryRow(ctx, `SELECT id FROM invoices WHERE tenant_id = $1 AND fee_key = $2`, tenantID, key).Scan(&existing)
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
	env := "live"
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
		env = "sandbox"
	}
	logoKey := ""
	if id := t.GetInvoiceLogoAssetId(); id != "" {
		logoKey = tenantID.String() + "/assets/" + id
	}
	paymentMethod := cu.GetCustomer().GetPaymentMethod()
	word := "monthly"
	if sub.GetInterval() == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
		word = "yearly"
	}
	line := rating.Line{Kind: rating.KindBaseFee, AmountMinor: d.GetFeeMinor(),
		Description: fmt.Sprintf("Base fee (%s) — %s — %s to %s", word, sub.GetPlanCode(), start.In(loc).Format("2006-01-02"), end.In(loc).Format("2006-01-02"))}
	snap, err := json.Marshal(saga.Snapshot{
		CustomerExternalID: sub.GetCustomerExternalId(), CustomerName: cu.GetCustomer().GetName(), CustomerEmail: cu.GetCustomer().GetEmail(),
		PaymentMethod: paymentMethod, Provider: sub.GetProvider(), TenantName: t.GetName(), Environment: env, CurrencyExponent: cur.Exponent, LogoObjectKey: logoKey,
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
			VALUES ($1,$2,$3,$4,'FEE',$5,$6,$7,$7,'draft',$8,$9,$10,$11) ON CONFLICT DO NOTHING RETURNING id`,
			tenantID, id, cuid, period.PeriodOf(start, loc), sub.GetPlanVersionId(), currency, d.GetFeeMinor(), snap, sub.GetProvider(), country, key).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // another pass won the race; the lookup below finds its invoice
		}
		if err != nil {
			return err
		}
		credit, err := applyCredits(ctx, tx, tenantID, cuid, id, d.GetFeeMinor())
		if err != nil {
			return err
		}
		lines := []rating.Line{line}
		if credit > 0 {
			lines = append(lines, rating.Line{Kind: rating.KindCredit, AmountMinor: -credit, Description: "Credit from a price correction of an earlier period"})
			if _, err := tx.Exec(ctx, `UPDATE invoices SET subtotal_minor = $3, total_minor = $3 WHERE tenant_id = $1 AND id = $2`, tenantID, id, d.GetFeeMinor()-credit); err != nil {
				return err
			}
		}
		for i, l := range lines {
			if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines (tenant_id, invoice_id, line_no, kind, amount_minor, description) VALUES ($1,$2,$3,$4,$5,$6)`,
				tenantID, id, i+1, string(l.Kind), l.AmountMinor, l.Description); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT id FROM invoices WHERE tenant_id = $1 AND fee_key = $2`, tenantID, key).Scan(&existing); err != nil {
		return uuid.Nil, err
	}
	m.invoices.Add(ctx, 1)
	return existing, nil
}
