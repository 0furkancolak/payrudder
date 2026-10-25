package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// billAdjustments turns what plan changes moved into money: an invoice when the new plan costs more than the unused old time was worth,
// a credit balance when it costs less. Each adjustment is handled once (its invoice or credit is found again by key) and then marked.
func (c *Commands) billAdjustments(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location) error {
	tctx := inputs.TenantCtx(ctx, tenantID)
	resp, err := c.cfg.Inputs.Plans.ListSubscriptionAdjustments(tctx, &plansv1.ListSubscriptionAdjustmentsRequest{PageSize: store.I32(c.cfg.PageSize)})
	if err != nil {
		return fmt.Errorf("list adjustments: %w", err)
	}
	for _, a := range resp.GetAdjustments() {
		if err := c.billAdjustment(ctx, tenantID, t, loc, a); err != nil {
			c.cfg.Log.WarnContext(ctx, "adjustment not billed", "tenant", tenantID, "adjustment", a.GetId(), "error", err)
		}
	}
	return nil
}

func (c *Commands) billAdjustment(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, a *plansv1.Adjustment) error {
	sub := a.GetSubscription()
	net := a.GetChargeMinor() - a.GetCreditMinor()
	switch {
	case net > 0:
		from, to := a.GetFromTime().AsTime(), a.GetToTime().AsTime()
		lines := []rating.Line{baseFeeLine(sub, a.GetChargeMinor(), from, to, loc)}
		if a.GetCreditMinor() > 0 {
			// The unused time of the old plan lowers what was sold, so it lowers the tax base too; a credit balance would not.
			lines = append(lines, rating.Line{Kind: rating.KindDiscount, AmountMinor: -a.GetCreditMinor(), ServiceFrom: from, ServiceTo: to,
				Description: fmt.Sprintf("Unused time on %s — %s to %s", a.GetOldPlanCode(), day(from, loc), day(to, loc))})
		}
		id, err := c.draftInvoice(ctx, tenantID, t, loc, invoiceSpec{Key: "adj|" + a.GetId(), Sub: sub, PeriodStart: from, Lines: lines})
		if err != nil {
			return err
		}
		if err := c.cfg.Invoices.Finalize(ctx, tenantID, id); err != nil {
			return err
		}
	case net < 0:
		cuid, err := uuid.Parse(sub.GetCustomerId())
		if err != nil {
			return err
		}
		adjustment, err := uuid.Parse(a.GetId())
		if err != nil {
			return err
		}
		if err := c.q.InsertCreditBalanceForAdjustment(ctx, sqlc.InsertCreditBalanceForAdjustmentParams{TenantID: tenantID, CustomerID: cuid, ID: uuid.New(), AmountMinor: -net,
			SourceAdjustmentID: adjustment, Currency: inputs.OrDefault(sub.GetCurrency(), t.GetCurrency())}); err != nil {
			return err
		}
	}
	_, err := c.cfg.Inputs.Plans.MarkSubscriptionAdjustmentBilled(inputs.TenantCtx(ctx, tenantID), &plansv1.MarkSubscriptionAdjustmentBilledRequest{AdjustmentId: a.GetId()})
	return err
}
