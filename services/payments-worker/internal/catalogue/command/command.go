// Package command makes a provider hold what Payrudder sells: the product of a plan, a recurring price for each interval and currency, and discounts. What
// the provider created is remembered, so doing the same again creates nothing twice.
package command

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/catalogue/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Plans is what the commands ask of plans-svc, as the tenant.
type Plans interface {
	GetPlanVersion(ctx context.Context, tenant uuid.UUID, req *plansv1.GetPlanVersionRequest) (*plansv1.PlanVersion, error)
	// TenantCurrency is the currency of base_fee_minor.
	TenantCurrency(ctx context.Context, tenant uuid.UUID) (string, error)
}

// Deps is what the commands need. Plans nil means plans-svc is not configured.
type Deps struct {
	Plans   Plans
	Objects store.Objects
	Log     *slog.Logger
}

// NeedPlans refuses with 503 when plans-svc is not configured.
func (d Deps) NeedPlans() error {
	if d.Plans == nil {
		return refuse.New(http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
	}
	return nil
}

// Sync asks a provider to hold a plan version.
type Sync struct {
	PlanCode    string `json:"plan_code"`
	PlanVersion int32  `json:"plan_version"`
	ProductName string `json:"product_name"`
	TaxCategory string `json:"tax_category"`
}

func (in Sync) productName() string {
	if in.ProductName == "" {
		return in.PlanCode
	}
	return in.ProductName
}

// Synced is what the provider holds for the plan version now.
type Synced struct {
	PlanVersionID string
	ProductRef    string
	// Created maps the key of each price made now to the provider's id; Existing those that were there; Skipped those the provider cannot bill, with why.
	Created  map[string]string
	Existing map[string]string
	Skipped  map[string]string
}

// SyncCatalogue makes the provider hold a plan version: the plan's product once, and a recurring price for each interval and currency the plan version has a
// fee in. What is there already is left alone, so syncing twice is the same as syncing once. A price in a currency the provider cannot bill is reported, not
// an error.
func SyncCatalogue(ctx context.Context, d Deps, tg target.Target, cat payments.Catalogue, in Sync) (Synced, error) {
	if in.PlanCode == "" {
		return Synced{}, refuse.New(http.StatusBadRequest, "payments.plan.code_required", "plan_code is required")
	}
	pv, err := d.Plans.GetPlanVersion(ctx, tg.Tenant, &plansv1.GetPlanVersionRequest{PlanCode: in.PlanCode, Version: in.PlanVersion})
	if err != nil {
		return Synced{}, &refuse.Plans{Err: err}
	}
	tenantCurrency, err := d.Plans.TenantCurrency(ctx, tg.Tenant)
	if err != nil {
		return Synced{}, &refuse.Plans{Err: err}
	}
	specs, existing, err := missingPrices(ctx, d, tg, in, pv, tenantCurrency)
	if err != nil {
		return Synced{}, fmt.Errorf("list the prices the provider holds: %w", err)
	}
	productKey := "plan:" + in.PlanCode
	productRef, _, err := d.Objects.Get(ctx, tg.Tenant, tg.Provider, tg.Env, query.KindProduct, productKey)
	if err != nil {
		return Synced{}, fmt.Errorf("find the provider's product: %w", err)
	}
	res := payments.CatalogueResult{ProductRef: productRef, Prices: map[string]string{}, Skipped: map[string]string{}}
	var syncErr error
	if len(specs) > 0 {
		res, syncErr = cat.SyncCatalogue(ctx, payments.CatalogueRequest{ProductRef: productRef, ProductName: in.productName(), TaxCategory: in.TaxCategory, PayrudderPlanCode: in.PlanCode, Prices: specs})
	}
	remember(ctx, d, tg, productKey, productRef, res)
	if syncErr != nil {
		return Synced{}, &refuse.Provider{What: "catalogue sync", Err: syncErr}
	}
	return Synced{PlanVersionID: pv.GetId(), ProductRef: res.ProductRef, Created: res.Prices, Existing: existing, Skipped: res.Skipped}, nil
}

// missingPrices lists the prices a plan version needs that the provider does not hold yet, and the refs of those it does.
func missingPrices(ctx context.Context, d Deps, tg target.Target, in Sync, pv *plansv1.PlanVersion, tenantCurrency string) ([]payments.PriceSpec, map[string]string, error) {
	type fee struct {
		interval, currency string
		amount             int64
	}
	fees := []fee{{"month", tenantCurrency, pv.GetBaseFeeMinor()}}
	for _, f := range pv.GetFees() {
		fees = append(fees, fee{subscription.IntervalFromProto(f.GetInterval()).Word(), f.GetCurrency(), f.GetBaseFeeMinor()})
	}
	existing := map[string]string{}
	var specs []payments.PriceSpec
	for _, f := range fees {
		if f.amount <= 0 {
			continue // a free plan needs no price
		}
		key := query.PriceKey(pv.GetId(), f.interval, f.currency)
		ref, found, err := d.Objects.Get(ctx, tg.Tenant, tg.Provider, tg.Env, query.KindPrice, key)
		if err != nil {
			return nil, nil, err
		}
		if found {
			existing[key] = ref
			continue
		}
		word := "monthly"
		if f.interval == "year" {
			word = "yearly"
		}
		specs = append(specs, payments.PriceSpec{Key: key, Currency: f.currency, AmountMinor: f.amount, Interval: f.interval, Name: fmt.Sprintf("%s (%s)", in.productName(), word)})
	}
	return specs, existing, nil
}

// remember keeps what was created, even when a later price failed, so the retry creates only the rest.
func remember(ctx context.Context, d Deps, tg target.Target, productKey, productRef string, res payments.CatalogueResult) {
	if res.ProductRef != "" && res.ProductRef != productRef {
		if err := d.Objects.Put(ctx, tg.Tenant, tg.Provider, tg.Env, query.KindProduct, productKey, res.ProductRef); err != nil {
			d.Log.ErrorContext(ctx, "could not remember a product created at the provider", "provider", tg.Provider, "ref", res.ProductRef, "err", err)
		}
	}
	for key, ref := range res.Prices {
		if err := d.Objects.Put(ctx, tg.Tenant, tg.Provider, tg.Env, query.KindPrice, key, ref); err != nil {
			d.Log.ErrorContext(ctx, "could not remember a price created at the provider", "provider", tg.Provider, "ref", ref, "err", err)
		}
	}
}

// CreateDiscount makes a coupon at the provider and remembers it by its code (or by the provider's id when it has none).
func CreateDiscount(ctx context.Context, d Deps, tg target.Target, api payments.DiscountAPI, spec payments.DiscountSpec) (payments.DiscountRefs, error) {
	refs, err := api.CreateDiscount(ctx, spec)
	if err != nil {
		return payments.DiscountRefs{}, &refuse.Provider{What: "create discount", Err: err}
	}
	key := refs.Code
	if key == "" {
		key = refs.DiscountRef
	}
	if err := d.Objects.Put(ctx, tg.Tenant, tg.Provider, tg.Env, query.KindDiscount, key, refs.DiscountRef); err != nil {
		d.Log.ErrorContext(ctx, "could not remember a discount created at the provider", "provider", tg.Provider, "ref", refs.DiscountRef, "err", err)
	}
	return refs, nil
}
