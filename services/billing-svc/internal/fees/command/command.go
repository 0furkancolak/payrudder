// Package command bills what is invoiced outside a billing run: the fixed fees that are due in advance and what plan changes moved. Each becomes a FEE invoice
// (or a credit balance) that is finalized at once, so its saga charges it.
package command

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Config is what the commands run on.
type Config struct {
	DB       *tenantdb.DB // billing_system
	Inputs   *inputs.Inputs
	Invoices *invoicecmd.Commands
	Now      func() time.Time
	// PageSize is how many subscriptions or adjustments one call to plans-svc reads (default 500).
	PageSize int
	Log      *slog.Logger
}

// Commands are the fee-billing commands.
type Commands struct {
	cfg Config
	q   *sqlc.Queries // only for WithTx: the statements run in the transactions DB opens
}

// New returns the commands; a missing clock is the wall clock, a missing page size 500 and a missing logger the default one.
func New(cfg Config) *Commands {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 500
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Commands{cfg: cfg, q: sqlc.New(cfg.DB.Pool())}
}

// Bill invoices the fixed fees that are due in advance, for every tenant: for each advance-billed subscription whose next period has
// started, one invoice for that period's fee (kind FEE), finalized so its saga charges it, and then the subscription's next fee period is
// advanced. Every step is idempotent (the invoice is found again by its fee_key, finalizing a finalized invoice and advancing an advanced
// period do nothing), so a crash at any point is repaired by the next pass. A paused subscription's period is skipped without an invoice.
//
// The tenants come from plans-svc, one by one, and each one's rows are reached where the directory says they are (ADR 0018): the run is per tenant, in either mode. A
// tenant that is being moved is left for the next pass.
func (c *Commands) Bill(ctx context.Context) error {
	var firstErr error
	err := c.cfg.Inputs.EachBillableTenant(ctx, func(id uuid.UUID, _ *plansv1.Tenant) error {
		if err := c.billTenant(ctx, id); tenantdb.Skippable(err) {
			c.cfg.Log.InfoContext(ctx, "scheduler: fee billing waits for the tenant", "tenant", id, "error", err)
		} else if err != nil {
			c.cfg.Log.WarnContext(ctx, "scheduler: fee billing failed", "tenant", id, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
		return nil // one tenant's problem must not hold back the others
	})
	if err != nil {
		return err
	}
	return firstErr
}

func (c *Commands) billTenant(ctx context.Context, tenantID uuid.UUID) error {
	// Before plans-svc is asked to apply anything for the tenant: a tenant that is moving is not touched at all.
	if err := c.cfg.DB.Check(ctx, tenantID); err != nil {
		return err
	}
	t, err := c.cfg.Inputs.Tenant(ctx, tenantID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return err
	}
	tctx := inputs.TenantCtx(ctx, tenantID)
	// Changes that were waiting for the end of a period come first: the new plan's first fee is due in the same pass.
	if _, err := c.cfg.Inputs.Plans.ApplyScheduledChanges(tctx, &plansv1.ApplyScheduledChangesRequest{Now: timestamppb.New(c.cfg.Now())}); err != nil {
		return fmt.Errorf("apply scheduled changes: %w", err)
	}
	if err := c.billAdjustments(ctx, tenantID, t, loc); err != nil {
		return err
	}
	after := ""
	for {
		page, err := c.cfg.Inputs.Plans.ListSubscriptionsFeeDue(tctx, &plansv1.ListSubscriptionsFeeDueRequest{Now: timestamppb.New(c.cfg.Now()), After: after, PageSize: store.I32(c.cfg.PageSize)})
		if err != nil {
			return fmt.Errorf("list fees due: %w", err)
		}
		if len(page.GetDue()) == 0 {
			return nil
		}
		for _, d := range page.GetDue() {
			if err := c.billFee(ctx, tenantID, t, loc, d); err != nil {
				// One subscription's problem must not hold back the others; it is retried on the next pass.
				c.cfg.Log.WarnContext(ctx, "fee not billed", "tenant", tenantID, "subscription", d.GetSubscription().GetId(), "error", err)
			}
		}
		after = page.GetDue()[len(page.GetDue())-1].GetSubscription().GetId()
		if len(page.GetDue()) < c.cfg.PageSize {
			return nil
		}
	}
}

// billFee invoices one fee period of a subscription (unless it is paused or free) and advances the subscription to the next one.
func (c *Commands) billFee(ctx context.Context, tenantID uuid.UUID, t *plansv1.Tenant, loc *time.Location, d *plansv1.FeeDue) error {
	sub := d.GetSubscription()
	start := d.GetPeriodStart().AsTime()
	if sub.GetState() != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED && d.GetFeeMinor() > 0 {
		end := d.GetPeriodEnd().AsTime()
		lines := []rating.Line{baseFeeLine(sub, d.GetFeeMinor(), start, end, loc)}
		if d.GetDiscountMinor() > 0 {
			lines = append(lines, rating.Line{Kind: rating.KindDiscount, AmountMinor: -d.GetDiscountMinor(), ServiceFrom: start, ServiceTo: end, Description: "Coupon " + d.GetCouponCode(), CouponCode: d.GetCouponCode()})
		}
		id, err := c.draftInvoice(ctx, tenantID, t, loc, invoiceSpec{
			Key: sub.GetId() + "|" + start.UTC().Format(time.RFC3339), Sub: sub, PeriodStart: start, Lines: lines})
		if err != nil {
			return err
		}
		if err := c.cfg.Invoices.Finalize(ctx, tenantID, id); err != nil {
			return err
		}
	}
	_, err := c.cfg.Inputs.Plans.AdvanceSubscriptionFee(inputs.TenantCtx(ctx, tenantID), &plansv1.AdvanceSubscriptionFeeRequest{
		SubscriptionId: sub.GetId(), PeriodStart: timestamppb.New(start)})
	return err
}

// baseFeeLine is the line of a fixed fee for the period from..to.
func baseFeeLine(sub *plansv1.Subscription, amountMinor int64, from, to time.Time, loc *time.Location) rating.Line {
	seats := ""
	if q := sub.GetQuantity(); q > 1 {
		seats = fmt.Sprintf(" × %d seats", q)
	}
	return rating.Line{Kind: rating.KindBaseFee, AmountMinor: amountMinor, ServiceFrom: from, ServiceTo: to,
		Description: fmt.Sprintf("Base fee (%s)%s — %s — %s to %s", feeWord(sub), seats, sub.GetPlanCode(), day(from, loc), day(to, loc))}
}

func feeWord(sub *plansv1.Subscription) string {
	if sub.GetInterval() == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
		return "yearly"
	}
	return "monthly"
}

func day(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }
