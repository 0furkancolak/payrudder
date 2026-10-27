package sandbox

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Tables are the tables of plans_db a reset deletes from; Kept are the tables with tenant rows it leaves alone on purpose: the sandbox's configuration, its keys and
// the record of resets themselves.
var (
	Tables = []string{"affiliate_reversals", "affiliate_commissions", "affiliate_journal", "affiliate_payouts", "affiliate_attributions", "affiliate_attribution_rejections", "affiliate_clicks_daily", "customer_coupons", "customer_entitlement_overrides", "coupon_redemptions", "subscription_adjustments", "subscription_changes", "subscription_coupons", "provider_migrations", "subscription_trial_notices",
		"subscriptions", "customers", "webhook_deliveries", "webhook_events", "outbox"}
	Kept = []string{"meters", "plans", "plan_versions", "plan_prices", "plan_entitlements", "plan_version_fees", "plan_currency_prices", "routing_rules", "tax_rates",
		"affiliate_programs", "affiliates", "coupons", "promotion_codes", "webhook_endpoints", "tenant_mail_settings", "tenant_mail_integrations", "api_keys", "sandbox_resets"}
)

// Local is plans-svc's own part of a reset, on the system pool: each statement names the tenant.
type Local struct{ pool *pgxpool.Pool }

// NewLocal returns plans-svc's step.
func NewLocal(systemPool *pgxpool.Pool) *Local { return &Local{pool: systemPool} }

// Step is the step "plans". It goes first: with its customers and subscriptions gone nothing can bill the tenant while the other services are emptied.
func (l *Local) Step() Step { return Step{Name: "plans", Run: l.Reset} }

// Reset deletes the tenant's customers, subscriptions and history in one transaction and zeroes the redemption counters of its coupons.
func (l *Local) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	err := pgxx.InTx(ctx, l.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		for _, s := range []struct {
			table string
			run   func(context.Context, uuid.UUID) (int64, error)
		}{
			{"affiliate_reversals", q.ResetAffiliateReversals}, {"affiliate_commissions", q.ResetAffiliateCommissions}, {"affiliate_journal", q.ResetAffiliateJournal},
			{"affiliate_payouts", q.ResetAffiliatePayouts}, {"affiliate_attributions", q.ResetAffiliateAttributions},
			{"affiliate_attribution_rejections", q.ResetAffiliateAttributionRejections}, {"affiliate_clicks_daily", q.ResetAffiliateClicks},
			{"customer_coupons", q.ResetCustomerCoupons}, {"customer_entitlement_overrides", q.ResetEntitlementOverrides}, {"coupon_redemptions", q.ResetCouponRedemptions}, {"subscription_adjustments", q.ResetSubscriptionAdjustments},
			{"subscription_changes", q.ResetSubscriptionChanges}, {"subscription_coupons", q.ResetSubscriptionCoupons}, {"provider_migrations", q.ResetProviderMigrations}, {"subscription_trial_notices", q.ResetTrialNotices},
			{"subscriptions", q.ResetSubscriptions}, {"customers", q.ResetCustomers}, {"webhook_deliveries", q.ResetWebhookDeliveries}, {"webhook_events", q.ResetWebhookEvents},
			{"outbox", q.ResetEntitlementOutbox},
		} {
			n, err := s.run(ctx, tenantID)
			if err != nil {
				return err
			}
			if n > 0 {
				out[s.table] = n
			}
		}
		if err := q.ResetCouponCounters(ctx, tenantID); err != nil {
			return err
		}
		return q.ResetPromotionCodeCounters(ctx, tenantID)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
