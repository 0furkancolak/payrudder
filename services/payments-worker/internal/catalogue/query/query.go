// Package query reads what Payrudder created at a provider: its products, prices and discounts. It never writes.
package query

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Kinds of object Payrudder creates at a provider.
const (
	KindProduct  = "product"
	KindPrice    = "price"
	KindDiscount = "discount"
)

// List is what Payrudder created at the provider for the tenant: products, prices and discounts.
func List(ctx context.Context, objects store.Objects, tg target.Target) ([]store.Object, error) {
	list, err := objects.List(ctx, tg.Tenant, tg.Provider, tg.Env, KindProduct, KindPrice, KindDiscount)
	if err != nil {
		return nil, fmt.Errorf("list provider objects: %w", err)
	}
	return list, nil
}

// PriceKey is Payrudder's name for one price of a plan version.
func PriceKey(planVersionID, interval, currency string) string {
	return planVersionID + "|" + interval + "|" + strings.ToUpper(currency)
}

// IntervalName is the word the price keys use for a subscription interval.
func IntervalName(i plansv1.SubscriptionInterval) string {
	if i == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
		return "year"
	}
	return "month"
}

func intervalFromName(name string) plansv1.SubscriptionInterval {
	if name == "year" {
		return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	}
	return plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
}

// PriceTarget finds the plan version and interval a provider's price ids stand for, if Payrudder created them. A price that cannot be looked up counts as
// not ours, and is logged: the subscription is then kept as it is rather than followed to a plan version.
func PriceTarget(ctx context.Context, log *slog.Logger, objects store.Objects, tenant uuid.UUID, provider, env string, refs []string) (string, plansv1.SubscriptionInterval, bool) {
	for _, ref := range refs {
		key, ok, err := objects.ByRef(ctx, tenant, provider, env, KindPrice, ref)
		if err != nil {
			log.WarnContext(ctx, "provider price not looked up", "provider", provider, "ref", ref, "err", err)
			continue
		}
		if !ok {
			continue
		}
		parts := strings.Split(key, "|")
		if len(parts) == 3 {
			return parts[0], intervalFromName(parts[1]), true
		}
	}
	return "", plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED, false
}
