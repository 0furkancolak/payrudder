package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments"
)

// The operator API over what a provider can do beyond charging: its catalogue, customers, subscriptions, discounts and documents. Each
// endpoint works only when the tenant has an account at the provider and the provider's adapter implements the interface it needs.

func (s *Server) apiRoutes(mux *http.ServeMux) {
	const base = "/admin/tenants/{tenant}/providers/{provider}"
	mux.HandleFunc("POST "+base+"/catalogue/sync", s.admin(s.syncCatalogue))
	mux.HandleFunc("GET "+base+"/catalogue", s.admin(s.listCatalogue))
	mux.HandleFunc("POST "+base+"/customers/{external_id}", s.admin(s.ensureCustomer))
	mux.HandleFunc("POST "+base+"/customers/{external_id}/portal", s.admin(s.portalLink))
	mux.HandleFunc("POST "+base+"/subscriptions/{id}/change-preview", s.admin(s.previewChange))
	mux.HandleFunc("POST "+base+"/subscriptions/{id}/change", s.admin(s.applyChange))
	mux.HandleFunc("POST "+base+"/subscriptions/{id}/pause", s.admin(s.pause))
	mux.HandleFunc("POST "+base+"/subscriptions/{id}/resume", s.admin(s.resume))
	mux.HandleFunc("POST "+base+"/subscriptions/{id}/payment-method-link", s.admin(s.paymentMethodLink))
	mux.HandleFunc("POST "+base+"/discounts", s.admin(s.createDiscount))
	mux.HandleFunc("GET "+base+"/transactions/{ref}/invoice", s.admin(s.invoiceLink))
}

// target is the tenant, environment and provider an API call is about.
type target struct {
	tenant   uuid.UUID
	env      string
	provider string
	prov     payments.Provider
}

func (s *Server) target(w http.ResponseWriter, r *http.Request) (target, bool) {
	t, ok := s.tenant(w, r)
	if !ok {
		return target{}, false
	}
	env, ok := environment(w, r)
	if !ok {
		return target{}, false
	}
	name := r.PathValue("provider")
	prov, err := s.Resolver.Named(r.Context(), t, name, env)
	if err != nil {
		fail(w, http.StatusNotFound, "payments.account.not_found", "no account at "+name+" for this tenant")
		return target{}, false
	}
	return target{tenant: t, env: env, provider: name, prov: prov}, true
}

// unsupported answers for a provider whose adapter lacks the interface an endpoint needs.
func unsupported(w http.ResponseWriter, provider, what string) {
	fail(w, http.StatusNotImplemented, "payments.provider.not_supported", provider+" does not support "+what)
}

// providerFail relays an error from a provider: a request it refused is the caller's to fix, anything else is a bad gateway.
func (s *Server) providerFail(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.Log.WarnContext(r.Context(), what+" failed", "err", err)
	if errors.Is(err, payments.ErrUnsupported) {
		fail(w, http.StatusConflict, "payments.provider.not_supported", err.Error())
		return
	}
	fail(w, http.StatusBadGateway, "payments.provider.failed", err.Error())
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "payments.request.bad_body", "the body must be JSON: "+err.Error())
		return false
	}
	return true
}

// priceKey is Quotify's name for one price of a plan version.
func priceKey(planVersionID, interval, currency string) string {
	return planVersionID + "|" + interval + "|" + strings.ToUpper(currency)
}

func intervalName(i plansv1.SubscriptionInterval) string {
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

// catalogueTarget finds the plan version and interval a provider's price ids stand for, if Quotify created them.
func (s *Server) catalogueTarget(ctx context.Context, tenant uuid.UUID, provider, env string, refs []string) (string, plansv1.SubscriptionInterval, bool) {
	for _, ref := range refs {
		key, ok, err := s.Objects.ByRef(ctx, tenant, provider, env, "price", ref)
		if err != nil || !ok {
			continue
		}
		parts := strings.Split(key, "|")
		if len(parts) == 3 {
			return parts[0], intervalFromName(parts[1]), true
		}
	}
	return "", plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED, false
}

// syncCatalogue makes the provider hold a plan version: the plan's product once, and a recurring price for each interval and currency the plan version has a
// fee in. What is there already is left alone, so syncing twice is the same as syncing once. A price in a currency the provider cannot bill is reported, not an error.
func (s *Server) syncCatalogue(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	cat, ok := tg.prov.(payments.Catalogue)
	if !ok {
		unsupported(w, tg.provider, "a catalogue")
		return
	}
	if s.Plans == nil {
		fail(w, http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
		return
	}
	var in catalogueInput
	if !decodeBody(w, r, &in) {
		return
	}
	if in.PlanCode == "" {
		fail(w, http.StatusBadRequest, "payments.plan.code_required", "plan_code is required")
		return
	}
	ctx := r.Context()
	pv, err := s.Plans.GetPlanVersion(ctx, tg.tenant, &plansv1.GetPlanVersionRequest{PlanCode: in.PlanCode, Version: in.PlanVersion})
	if err != nil {
		planFail(w, err)
		return
	}
	tenantCurrency, err := s.Plans.TenantCurrency(ctx, tg.tenant)
	if err != nil {
		planFail(w, err)
		return
	}
	specs, existing, err := s.missingPrices(ctx, tg, in, pv, tenantCurrency)
	if err != nil {
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	productKey := "plan:" + in.PlanCode
	productRef, _, err := s.Objects.Get(ctx, tg.tenant, tg.provider, tg.env, "product", productKey)
	if err != nil {
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	res := payments.CatalogueResult{ProductRef: productRef, Prices: map[string]string{}, Skipped: map[string]string{}}
	var syncErr error
	if len(specs) > 0 {
		res, syncErr = cat.SyncCatalogue(ctx, payments.CatalogueRequest{ProductRef: productRef, ProductName: in.productName(), TaxCategory: in.TaxCategory, QuotifyPlanCode: in.PlanCode, Prices: specs})
	}
	s.rememberCatalogue(ctx, tg, productKey, productRef, res)
	if syncErr != nil {
		s.providerFail(w, r, "catalogue sync", syncErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan_version_id": pv.GetId(), "product_ref": res.ProductRef, "created": res.Prices, "existing": existing, "skipped": res.Skipped})
}

type catalogueInput struct {
	PlanCode    string `json:"plan_code"`
	PlanVersion int32  `json:"plan_version"`
	ProductName string `json:"product_name"`
	TaxCategory string `json:"tax_category"`
}

func (in catalogueInput) productName() string {
	if in.ProductName == "" {
		return in.PlanCode
	}
	return in.ProductName
}

// missingPrices lists the prices a plan version needs that the provider does not hold yet, and the refs of those it does.
func (s *Server) missingPrices(ctx context.Context, tg target, in catalogueInput, pv *plansv1.PlanVersion, tenantCurrency string) ([]payments.PriceSpec, map[string]string, error) {
	type fee struct {
		interval, currency string
		amount             int64
	}
	fees := []fee{{"month", tenantCurrency, pv.GetBaseFeeMinor()}}
	for _, f := range pv.GetFees() {
		fees = append(fees, fee{intervalName(f.GetInterval()), f.GetCurrency(), f.GetBaseFeeMinor()})
	}
	existing := map[string]string{}
	var specs []payments.PriceSpec
	for _, f := range fees {
		if f.amount <= 0 {
			continue // a free plan needs no price
		}
		key := priceKey(pv.GetId(), f.interval, f.currency)
		ref, found, err := s.Objects.Get(ctx, tg.tenant, tg.provider, tg.env, "price", key)
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

// rememberCatalogue keeps what was created, even when a later price failed, so the retry creates only the rest.
func (s *Server) rememberCatalogue(ctx context.Context, tg target, productKey, productRef string, res payments.CatalogueResult) {
	if res.ProductRef != "" && res.ProductRef != productRef {
		if err := s.Objects.Put(ctx, tg.tenant, tg.provider, tg.env, "product", productKey, res.ProductRef); err != nil {
			s.Log.ErrorContext(ctx, "could not remember a product created at the provider", "provider", tg.provider, "ref", res.ProductRef, "err", err)
		}
	}
	for key, ref := range res.Prices {
		if err := s.Objects.Put(ctx, tg.tenant, tg.provider, tg.env, "price", key, ref); err != nil {
			s.Log.ErrorContext(ctx, "could not remember a price created at the provider", "provider", tg.provider, "ref", ref, "err", err)
		}
	}
}

func (s *Server) listCatalogue(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	list, err := s.Objects.List(r.Context(), tg.tenant, tg.provider, tg.env, "product", "price", "discount")
	if err != nil {
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": list})
}

// ensureCustomer makes sure the customer exists at the provider (and has an address for its country), and remembers the provider's ids.
func (s *Server) ensureCustomer(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.CustomerAPI)
	if !ok {
		unsupported(w, tg.provider, "customers")
		return
	}
	refs, status, err := s.customerRefs(r.Context(), tg, api, r.PathValue("external_id"))
	if err != nil {
		if status == 0 {
			s.providerFail(w, r, "ensure customer", err)
		} else {
			fail(w, status, "payments.customer.not_ready", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"customer_ref": refs.CustomerRef, "address_ref": refs.AddressRef, "business_ref": refs.BusinessRef})
}

// customerRefs finds or creates the customer at the provider. A zero status means the provider failed.
func (s *Server) customerRefs(ctx context.Context, tg target, api payments.CustomerAPI, externalID string) (payments.CustomerRefs, int, error) {
	if s.Plans == nil {
		return payments.CustomerRefs{}, http.StatusServiceUnavailable, errors.New("plans-svc is not configured")
	}
	c, err := s.Plans.GetCustomer(ctx, tg.tenant, externalID)
	if err != nil {
		return payments.CustomerRefs{}, http.StatusNotFound, errors.New("no such customer")
	}
	refs, err := api.EnsureCustomer(ctx, payments.CustomerSpec{QuotifyCustomerID: c.GetId(), Email: c.GetEmail(), Name: c.GetName(), Country: c.GetCountry()})
	if err != nil {
		return refs, 0, err
	}
	for kind, ref := range map[string]string{"customer": refs.CustomerRef, "address": refs.AddressRef, "business": refs.BusinessRef} {
		if ref != "" {
			if err := s.Objects.Put(ctx, tg.tenant, tg.provider, tg.env, kind, "cus:"+c.GetId(), ref); err != nil {
				s.Log.ErrorContext(ctx, "could not remember a customer created at the provider", "provider", tg.provider, "ref", ref, "err", err)
			}
		}
	}
	return refs, 0, nil
}

// portalLink returns the provider's page where the customer manages payment methods and sees invoices.
func (s *Server) portalLink(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.CustomerAPI)
	if !ok {
		unsupported(w, tg.provider, "a customer portal")
		return
	}
	refs, status, err := s.customerRefs(r.Context(), tg, api, r.PathValue("external_id"))
	if err != nil {
		if status == 0 {
			s.providerFail(w, r, "ensure customer", err)
		} else {
			fail(w, status, "payments.customer.not_ready", err.Error())
		}
		return
	}
	url, err := api.PortalLink(r.Context(), refs.CustomerRef, nil)
	if err != nil {
		s.providerFail(w, r, "portal link", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// changeRequest is the body of a change: the same words as the tenant API's change request.
type changeRequest struct {
	PlanCode    string `json:"plan_code"`
	PlanVersion int32  `json:"plan_version"`
	Interval    string `json:"interval"`
	Timing      string `json:"timing"`
	Proration   string `json:"proration"`
}

func enumWord(v, prefix string) string { return strings.TrimPrefix(strings.ToLower(v), prefix) }

// providerSubscription loads a subscription Quotify mirrors and checks the provider runs it.
func (s *Server) providerSubscription(w http.ResponseWriter, r *http.Request, tg target) (*plansv1.Subscription, bool) {
	if s.Plans == nil {
		fail(w, http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
		return nil, false
	}
	sub, err := s.Plans.GetSubscription(r.Context(), tg.tenant, r.PathValue("id"))
	if err != nil {
		planFail(w, err)
		return nil, false
	}
	if sub.GetOwner() != "provider" || sub.GetProvider() != tg.provider || sub.GetProviderRef() == "" {
		fail(w, http.StatusConflict, "payments.subscription.wrong_provider", "this subscription is not run by "+tg.provider)
		return nil, false
	}
	return sub, true
}

// planChange works out the provider's change for a request: the price of the target plan version and interval, which must be in the catalogue.
func (s *Server) planChange(ctx context.Context, tg target, sub *plansv1.Subscription, in changeRequest) (payments.ProviderChange, string, plansv1.SubscriptionInterval, int, error) {
	pvID := sub.GetPlanVersionId()
	if in.PlanCode != "" {
		pv, err := s.Plans.GetPlanVersion(ctx, tg.tenant, &plansv1.GetPlanVersionRequest{PlanCode: in.PlanCode, Version: in.PlanVersion})
		if err != nil {
			return payments.ProviderChange{}, "", 0, http.StatusNotFound, errors.New("no such plan version")
		}
		pvID = pv.GetId()
	}
	iv := sub.GetInterval()
	switch enumWord(in.Interval, "subscription_interval_") {
	case "year":
		iv = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	case "month":
		iv = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
	case "":
	default:
		return payments.ProviderChange{}, "", 0, http.StatusBadRequest, errors.New("interval must be month or year")
	}
	if pvID == sub.GetPlanVersionId() && iv == sub.GetInterval() {
		return payments.ProviderChange{}, "", 0, http.StatusBadRequest, errors.New("the subscription is already on this plan version and interval")
	}
	ref, found, err := s.Objects.Get(ctx, tg.tenant, tg.provider, tg.env, "price", priceKey(pvID, intervalName(iv), sub.GetCurrency()))
	if err != nil {
		return payments.ProviderChange{}, "", 0, http.StatusInternalServerError, errors.New("internal error")
	}
	if !found {
		return payments.ProviderChange{}, "", 0, http.StatusConflict, fmt.Errorf("%s has no %sly price in %s for this plan version; sync the catalogue first", tg.provider, intervalName(iv), sub.GetCurrency())
	}
	timing := enumWord(in.Timing, "change_timing_")
	if timing == "" {
		timing = "immediately"
	}
	return payments.ProviderChange{Items: []payments.ChangeItem{{PriceRef: ref, Quantity: 1}}, Timing: timing, Proration: enumWord(in.Proration, "change_proration_")}, pvID, iv, 0, nil
}

func (s *Server) previewChange(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.SubscriptionAPI)
	if !ok {
		unsupported(w, tg.provider, "driving subscriptions")
		return
	}
	var in changeRequest
	if !decodeBody(w, r, &in) {
		return
	}
	sub, ok := s.providerSubscription(w, r, tg)
	if !ok {
		return
	}
	ch, _, iv, code, err := s.planChange(r.Context(), tg, sub, in)
	if err != nil {
		fail(w, code, "payments.subscription.change_refused", err.Error())
		return
	}
	p, err := api.PreviewChange(r.Context(), sub.GetProviderRef(), ch)
	if err != nil {
		s.providerFail(w, r, "preview change", err)
		return
	}
	out := map[string]any{
		"timing": "CHANGE_TIMING_IMMEDIATELY", "currency": orCurrency(p.Currency, sub.GetCurrency()), "interval": iv.String(),
		"credit_minor": strconv.FormatInt(p.CreditMinor, 10), "charge_minor": strconv.FormatInt(p.ChargeMinor, 10), "net_minor": strconv.FormatInt(p.ResultMinor, 10),
		"next_renewal_minor": strconv.FormatInt(p.NextAmountMinor, 10), "by_provider": true,
	}
	if p.NextBilledAt != nil {
		out["next_renewal_time"] = p.NextBilledAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

func orCurrency(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// applyChange changes the subscription at the provider and then lets Quotify's copy follow at once. If the provider took the change and plans-svc
// could not follow, the provider's notification will make it follow, which the answer says.
func (s *Server) applyChange(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.SubscriptionAPI)
	if !ok {
		unsupported(w, tg.provider, "driving subscriptions")
		return
	}
	var in changeRequest
	if !decodeBody(w, r, &in) {
		return
	}
	sub, ok := s.providerSubscription(w, r, tg)
	if !ok {
		return
	}
	ctx := r.Context()
	ch, pvID, iv, code, err := s.planChange(ctx, tg, sub, in)
	if err != nil {
		fail(w, code, "payments.subscription.change_refused", err.Error())
		return
	}
	if err := api.ApplyChange(ctx, sub.GetProviderRef(), ch); err != nil {
		s.providerFail(w, r, "apply change", err)
		return
	}
	err = s.Plans.SyncProviderSubscription(ctx, tg.tenant, &plansv1.SyncProviderSubscriptionRequest{
		SubscriptionId: sub.GetId(), Provider: tg.provider, ProviderRef: sub.GetProviderRef(), Owner: "provider", OccurredAt: timestamppb.Now(), PlanVersionId: pvID, Interval: iv})
	if err != nil {
		s.Log.ErrorContext(ctx, "the provider changed a subscription but plans-svc did not follow", "subscription", sub.GetId(), "err", err)
		writeJSON(w, http.StatusAccepted, map[string]any{"changed_at_provider": true, "message": "the provider applied the change; Payrudder will follow when the provider's notification arrives"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed_at_provider": true, "followed": true})
}

func (s *Server) pause(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.SubscriptionAPI)
	if !ok {
		unsupported(w, tg.provider, "driving subscriptions")
		return
	}
	var in struct {
		AtPeriodEnd bool       `json:"at_period_end"`
		ResumeAt    *time.Time `json:"resume_at"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &in) {
		return
	}
	sub, ok := s.providerSubscription(w, r, tg)
	if !ok {
		return
	}
	if err := api.Pause(r.Context(), sub.GetProviderRef(), in.AtPeriodEnd, in.ResumeAt); err != nil {
		s.providerFail(w, r, "pause", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"requested": true, "note": "Payrudder's copy follows when the provider's notification arrives"})
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.SubscriptionAPI)
	if !ok {
		unsupported(w, tg.provider, "driving subscriptions")
		return
	}
	sub, ok := s.providerSubscription(w, r, tg)
	if !ok {
		return
	}
	if err := api.Resume(r.Context(), sub.GetProviderRef()); err != nil {
		s.providerFail(w, r, "resume", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"requested": true, "note": "Payrudder's copy follows when the provider's notification arrives"})
}

func (s *Server) paymentMethodLink(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.SubscriptionAPI)
	if !ok {
		unsupported(w, tg.provider, "driving subscriptions")
		return
	}
	sub, ok := s.providerSubscription(w, r, tg)
	if !ok {
		return
	}
	url, err := api.PaymentMethodLink(r.Context(), sub.GetProviderRef())
	if err != nil {
		s.providerFail(w, r, "payment method link", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) createDiscount(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.DiscountAPI)
	if !ok {
		unsupported(w, tg.provider, "discounts")
		return
	}
	var in payments.DiscountSpec
	if !decodeBody(w, r, &in) {
		return
	}
	refs, err := api.CreateDiscount(r.Context(), in)
	if err != nil {
		s.providerFail(w, r, "create discount", err)
		return
	}
	key := refs.Code
	if key == "" {
		key = refs.DiscountRef
	}
	if err := s.Objects.Put(r.Context(), tg.tenant, tg.provider, tg.env, "discount", key, refs.DiscountRef); err != nil {
		s.Log.ErrorContext(r.Context(), "could not remember a discount created at the provider", "provider", tg.provider, "ref", refs.DiscountRef, "err", err)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"discount_ref": refs.DiscountRef, "code": refs.Code})
}

func (s *Server) invoiceLink(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.target(w, r)
	if !ok {
		return
	}
	api, ok := tg.prov.(payments.DocumentLinker)
	if !ok {
		unsupported(w, tg.provider, "invoice documents")
		return
	}
	url, err := api.InvoiceURL(r.Context(), r.PathValue("ref"))
	if err != nil {
		s.providerFail(w, r, "invoice link", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}
