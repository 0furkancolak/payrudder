package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
)

func (s *Server) routes() []Route {
	return []Route{
		// Usage events
		{"POST", "/v1/events", AccessTenant, ratelimit.ClassIngest, s.ingestEvents},
		// Customers
		{"POST", "/v1/customers", AccessTenant, ratelimit.ClassWrite, s.createCustomer},
		{"GET", "/v1/customers", AccessTenant, ratelimit.ClassRead, s.listCustomers},
		{"GET", "/v1/customers/{external_id}", AccessTenant, ratelimit.ClassRead, s.getCustomer},
		{"GET", "/v1/customers/{external_id}/usage", AccessTenant, ratelimit.ClassRead, s.getCustomerUsage},
		{"GET", "/v1/customers/{external_id}/entitlements", AccessTenant, ratelimit.ClassRead, s.listEntitlements},
		{"GET", "/v1/customers/{external_id}/entitlements/{meter}", AccessTenant, ratelimit.ClassRead, s.getEntitlement},
		{"PUT", "/v1/customers/{external_id}/payment-method", AccessTenant, ratelimit.ClassWrite, s.setPaymentMethod},
		// Meters
		{"POST", "/v1/meters", AccessTenant, ratelimit.ClassWrite, s.createMeter},
		{"GET", "/v1/meters", AccessTenant, ratelimit.ClassRead, s.listMeters},
		{"GET", "/v1/meters/{code}", AccessTenant, ratelimit.ClassRead, s.getMeter},
		// Plans
		{"POST", "/v1/plans", AccessTenant, ratelimit.ClassWrite, s.createPlan},
		{"GET", "/v1/plans", AccessTenant, ratelimit.ClassRead, s.listPlans},
		{"POST", "/v1/plans/{code}/versions", AccessTenant, ratelimit.ClassWrite, s.publishPlanVersion},
		{"GET", "/v1/plans/{code}/versions/{version}", AccessTenant, ratelimit.ClassRead, s.getPlanVersion},
		// Subscriptions
		{"POST", "/v1/subscriptions", AccessTenant, ratelimit.ClassWrite, s.createSubscription},
		{"GET", "/v1/subscriptions", AccessTenant, ratelimit.ClassRead, s.listSubscriptions},
		{"GET", "/v1/subscriptions/{id}", AccessTenant, ratelimit.ClassRead, s.getSubscription},
		{"DELETE", "/v1/subscriptions/{id}", AccessTenant, ratelimit.ClassWrite, s.cancelSubscription},
		{"POST", "/v1/subscriptions/{id}/pause", AccessTenant, ratelimit.ClassWrite, s.pauseSubscription},
		{"POST", "/v1/subscriptions/{id}/resume", AccessTenant, ratelimit.ClassWrite, s.resumeSubscription},
		{"POST", "/v1/subscriptions/{id}/cancel-at-period-end", AccessTenant, ratelimit.ClassWrite, s.scheduleCancellation},
		{"DELETE", "/v1/subscriptions/{id}/cancel-at-period-end", AccessTenant, ratelimit.ClassWrite, s.withdrawCancellation},
		{"POST", "/v1/subscriptions/{id}/change-plan", AccessTenant, ratelimit.ClassWrite, s.changePlan},
		{"POST", "/v1/subscriptions/{id}/change-preview", AccessTenant, ratelimit.ClassRead, s.previewSubscriptionChange},
		{"POST", "/v1/subscriptions/{id}/change", AccessTenant, ratelimit.ClassWrite, s.changeSubscription},
		{"POST", "/v1/subscriptions/{id}/coupon", AccessTenant, ratelimit.ClassWrite, s.applyCoupon},
		{"DELETE", "/v1/subscriptions/{id}/coupon", AccessTenant, ratelimit.ClassWrite, s.removeCoupon},
		{"GET", "/v1/tax-rates", AccessTenant, ratelimit.ClassRead, s.getTaxRates},
		{"PUT", "/v1/tax-rates", AccessTenant, ratelimit.ClassWrite, s.setTaxRates},
		{"POST", "/v1/coupons", AccessTenant, ratelimit.ClassWrite, s.createCoupon},
		{"GET", "/v1/coupons", AccessTenant, ratelimit.ClassRead, s.listCoupons},
		{"DELETE", "/v1/coupons/{code}", AccessTenant, ratelimit.ClassWrite, s.deactivateCoupon},
		{"DELETE", "/v1/subscriptions/{id}/scheduled-change", AccessTenant, ratelimit.ClassWrite, s.cancelScheduledChange},
		// Webhooks: where Quotify sends what happens in the account
		{"POST", "/v1/webhook-endpoints", AccessTenant, ratelimit.ClassWrite, s.createWebhookEndpoint},
		{"GET", "/v1/webhook-endpoints", AccessTenant, ratelimit.ClassRead, s.listWebhookEndpoints},
		{"PUT", "/v1/webhook-endpoints/{id}", AccessTenant, ratelimit.ClassWrite, s.updateWebhookEndpoint},
		{"DELETE", "/v1/webhook-endpoints/{id}", AccessTenant, ratelimit.ClassWrite, s.deleteWebhookEndpoint},
		{"POST", "/v1/webhook-endpoints/{id}/rotate-secret", AccessTenant, ratelimit.ClassWrite, s.rotateWebhookSecret},
		{"POST", "/v1/webhook-endpoints/{id}/test", AccessTenant, ratelimit.ClassWrite, s.sendTestWebhook},
		{"GET", "/v1/webhook-deliveries", AccessTenant, ratelimit.ClassRead, s.listWebhookDeliveries},
		{"POST", "/v1/webhook-deliveries/{id}/redeliver", AccessTenant, ratelimit.ClassWrite, s.redeliverWebhook},
		// Reports
		{"GET", "/v1/reports/revenue", AccessTenant, ratelimit.ClassRead, s.revenueReport},
		{"GET", "/v1/dunning-settings", AccessTenant, ratelimit.ClassRead, s.getDunningSettings},
		{"PUT", "/v1/dunning-settings", AccessTenant, ratelimit.ClassWrite, s.setDunningSettings},
		{"GET", "/v1/reports/dunning", AccessTenant, ratelimit.ClassRead, s.dunningReport},
		{"GET", "/v1/reports/subscriptions", AccessTenant, ratelimit.ClassRead, s.subscriptionReport},
		// Payment provider routing
		{"GET", "/v1/routing-rules", AccessTenant, ratelimit.ClassRead, s.getRoutingRules},
		{"PUT", "/v1/routing-rules", AccessTenant, ratelimit.ClassWrite, s.setRoutingRules},
		{"POST", "/v1/routing-rules/preview", AccessTenant, ratelimit.ClassRead, s.previewRouting},
		// Assets: direct multipart sessions, a streamed form upload, and the files themselves
		{"POST", "/v1/assets/uploads", AccessTenant, ratelimit.ClassWrite, s.createUploadSession},
		{"POST", "/v1/assets/uploads/{session}/files/{asset}/parts", AccessTenant, ratelimit.ClassWrite, s.presignParts},
		{"GET", "/v1/assets/uploads/{session}/files/{asset}", AccessTenant, ratelimit.ClassRead, s.getUploadFile},
		{"POST", "/v1/assets/uploads/{session}/files/{asset}/complete", AccessTenant, ratelimit.ClassWrite, s.completeUploadFile},
		{"DELETE", "/v1/assets/uploads/{session}/files/{asset}", AccessTenant, ratelimit.ClassWrite, s.abortUploadFile},
		{"DELETE", "/v1/assets/uploads/{session}", AccessTenant, ratelimit.ClassWrite, s.abortUploadSession},
		{"PUT", "/v1/tenant/invoice-logo", AccessTenant, ratelimit.ClassWrite, s.setInvoiceLogo},
		{"DELETE", "/v1/tenant/invoice-logo", AccessTenant, ratelimit.ClassWrite, s.clearInvoiceLogo},
		{"POST", "/v1/assets", AccessTenant, ratelimit.ClassWrite, s.uploadAssets},
		{"GET", "/v1/assets", AccessTenant, ratelimit.ClassRead, s.listAssets},
		{"GET", "/v1/assets/{id}", AccessTenant, ratelimit.ClassRead, s.getAsset},
		{"GET", "/v1/assets/{id}/download", AccessTenant, ratelimit.ClassRead, s.downloadAsset},
		{"DELETE", "/v1/assets/{id}", AccessTenant, ratelimit.ClassWrite, s.deleteAsset},
		// Analytics (estimates: invoices come from billing)
		{"GET", "/v1/analytics/usage", AccessTenant, ratelimit.ClassRead, s.getAnalyticsUsage},
		{"GET", "/v1/analytics/top-customers", AccessTenant, ratelimit.ClassRead, s.getTopCustomers},
		// Invoices
		{"GET", "/v1/invoices", AccessTenant, ratelimit.ClassRead, s.listInvoices},
		{"GET", "/v1/invoices/{id}", AccessTenant, ratelimit.ClassRead, s.getInvoice},
		{"GET", "/v1/credit-notes", AccessTenant, ratelimit.ClassRead, s.listCreditNotes},
		{"GET", "/v1/credit-notes/{id}", AccessTenant, ratelimit.ClassRead, s.getCreditNote},
		{"GET", "/v1/invoices/{id}/pdf", AccessTenant, ratelimit.ClassRead, s.getInvoicePDF},
		// Sandbox
		{"POST", "/v1/sandbox/close-period", AccessSandbox, ratelimit.ClassWrite, s.sandboxClosePeriod},
		{"POST", "/v1/sandbox/customers/{external_id}/payment-scenario", AccessSandbox, ratelimit.ClassWrite, s.setPaymentScenario},
		// Operator
		{"POST", "/admin/v1/accounts", AccessOperator, "", s.createAccount},
		{"GET", "/admin/v1/tenants", AccessOperator, "", s.adminListTenants},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices", AccessOperator, "", s.adminListInvoices},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}", AccessOperator, "", s.adminGetInvoice},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/pdf", AccessOperator, "", s.adminInvoicePDF},
		{"GET", "/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/saga", AccessOperator, "", s.adminInvoiceSaga},
		{"GET", "/admin/v1/tenants/{tenant_id}/provider-accounts", AccessOperator, "", s.providerAccounts},
		{"PUT", "/admin/v1/tenants/{tenant_id}/provider-accounts/{provider}", AccessOperator, "", s.providerAccount},
		{"DELETE", "/admin/v1/tenants/{tenant_id}/provider-accounts/{provider}", AccessOperator, "", s.providerAccount},
		{"GET", "/admin/v1/tenants/{tenant_id}/ledger", AccessOperator, "", s.paymentLedger},
		{"GET", "/admin/v1/tenants/{tenant_id}/reconciliation", AccessOperator, "", s.paymentReconciliation},
		{"POST", "/webhooks/{provider}", AccessPublic, "", s.providerWebhook},
		{"POST", "/webhooks/{provider}/{tenant_id}/{environment}", AccessPublic, "", s.tenantWebhook},
		{"GET", "/admin/v1/tenants/{tenant_id}/provider-subscriptions", AccessOperator, "", s.providerSubscriptions},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/catalogue/sync", AccessOperator, "", s.providerAPI},
		{"GET", "/admin/v1/tenants/{tenant_id}/providers/{provider}/catalogue", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/customers/{external_id}/portal", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/change-preview", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/change", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/pause", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/resume", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/subscriptions/{id}/payment-method-link", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/providers/{provider}/discounts", AccessOperator, "", s.providerAPI},
		{"GET", "/admin/v1/tenants/{tenant_id}/providers/{provider}/transactions/{ref}/invoice", AccessOperator, "", s.providerAPI},
		{"POST", "/admin/v1/tenants/{tenant_id}/checkouts", AccessOperator, "", s.providerCheckout},
		{"POST", "/admin/v1/tenants/{tenant_id}/provider-migrations", AccessOperator, "", s.providerMigrate},
		{"POST", "/admin/v1/tenants/{tenant_id}/provider-migrations/{migration_id}/abort", AccessOperator, "", s.providerMigrationAbort},
		{"GET", "/admin/v1/dead-letters", AccessOperator, "", s.adminListDeadLetters},
		{"POST", "/admin/v1/dead-letters/redrive", AccessOperator, "", s.adminRedriveDeadLetters},
		{"GET", "/admin/v1/tenants/{id}", AccessOperator, "", s.getTenant},
		{"PATCH", "/admin/v1/tenants/{id}", AccessOperator, "", s.updateTenant},
		{"POST", "/admin/v1/tenants/{id}/api-keys", AccessOperator, "", s.createAPIKey},
		{"DELETE", "/admin/v1/api-keys/{id}", AccessOperator, "", s.revokeAPIKey},
		{"GET", "/admin/v1/analytics/ingest-stats", AccessOperator, "", s.adminIngestStats},
		{"POST", "/admin/v1/billing/close-period", AccessOperator, "", s.adminClosePeriod},
		{"GET", "/admin/v1/tenants/{tenant_id}/billing-runs/{period}", AccessOperator, "", s.adminGetBillingRun},
		{"POST", "/admin/v1/tenants/{tenant_id}/replays", AccessOperator, "", s.adminStartReplay},
		{"GET", "/admin/v1/tenants/{tenant_id}/replays/{job_id}", AccessOperator, "", s.adminGetReplay},
		{"POST", "/admin/v1/tenants/{tenant_id}/replays/{job_id}/promote", AccessOperator, "", s.adminPromoteReplay},
		{"POST", "/admin/v1/tenants/{tenant_id}/replays/{job_id}/discard", AccessOperator, "", s.adminDiscardReplay},
		{"POST", "/admin/v1/tenants/{tenant_id}/billing-runs/{period}/rerating", AccessOperator, "", s.adminStartRerating},
		{"GET", "/admin/v1/tenants/{tenant_id}/rerating-jobs/{job_id}", AccessOperator, "", s.adminGetRerating},
		{"POST", "/admin/v1/invoices/{id}/retry", AccessOperator, "", s.adminRetryInvoice},
		{"POST", "/admin/v1/invoices/{id}/void", AccessOperator, "", s.adminVoidInvoice},
		{"POST", "/admin/v1/invoices/{id}/credit-notes", AccessOperator, "", s.adminCreateCreditNote},
		{"POST", "/admin/v1/credit-notes/{id}/retry-refund", AccessOperator, "", s.adminRetryCreditNoteRefund},
	}
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateCustomerRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateCustomer(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetCustomer(), err)
}

func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ListCustomersRequest{PageToken: r.URL.Query().Get("page_token")}
	if v := r.URL.Query().Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Plans.ListCustomers(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetCustomer(r.Context(), &plansv1.GetCustomerRequest{ExternalId: r.PathValue("external_id")})
	respond(w, r, http.StatusOK, resp.GetCustomer(), err)
}

func (s *Server) setPaymentMethod(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetPaymentMethodRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.ExternalId = r.PathValue("external_id")
	resp, err := s.d.Plans.SetPaymentMethod(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetCustomer(), err)
}

// setPaymentScenario is the sandbox spelling of SetPaymentMethod: {"scenario": "declined"} → pm_sandbox_declined.
func (s *Server) setPaymentScenario(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scenario string `json:"scenario"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Scenario == "" {
		writeError(w, r, invalid("scenario", "required, e.g. \"declined\" or \"timeout_committed\""))
		return
	}
	resp, err := s.d.Plans.SetPaymentMethod(r.Context(), &plansv1.SetPaymentMethodRequest{
		ExternalId: r.PathValue("external_id"), PaymentMethod: "pm_sandbox_" + body.Scenario,
	})
	respond(w, r, http.StatusOK, resp.GetCustomer(), err)
}

func (s *Server) createMeter(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateMeterRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateMeter(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetMeter(), err)
}

func (s *Server) listMeters(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListMeters(r.Context(), &plansv1.ListMetersRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getMeter(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetMeter(r.Context(), &plansv1.GetMeterRequest{Code: r.PathValue("code")})
	respond(w, r, http.StatusOK, resp.GetMeter(), err)
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreatePlanRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreatePlan(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetPlan(), err)
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListPlans(r.Context(), &plansv1.ListPlansRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) publishPlanVersion(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.PublishPlanVersionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.PlanCode = r.PathValue("code")
	resp, err := s.d.Plans.PublishPlanVersion(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetPlanVersion(), err)
}

func (s *Server) getPlanVersion(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.GetPlanVersionRequest{PlanCode: r.PathValue("code")}
	if v := r.PathValue("version"); v != "latest" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 {
			writeError(w, r, invalid("version", `a positive integer or "latest"`))
			return
		}
		req.Version = int32(n)
	}
	resp, err := s.d.Plans.GetPlanVersion(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetPlanVersion(), err)
}

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateSubscriptionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateSubscription(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetSubscription(), err)
}

// listSubscriptions is GET /v1/subscriptions?customer=&page_size=&page_token=, newest first. customer is the external id.
func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &plansv1.ListSubscriptionsRequest{CustomerExternalId: q.Get("customer"), PageToken: q.Get("page_token")}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Plans.ListSubscriptions(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetSubscription(r.Context(), &plansv1.GetSubscriptionRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) pauseSubscription(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.PauseSubscription(r.Context(), &plansv1.PauseSubscriptionRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) resumeSubscription(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ResumeSubscription(r.Context(), &plansv1.ResumeSubscriptionRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) scheduleCancellation(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ScheduleCancellation(r.Context(), &plansv1.ScheduleCancellationRequest{SubscriptionId: r.PathValue("id"), Cancel: true})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) withdrawCancellation(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ScheduleCancellation(r.Context(), &plansv1.ScheduleCancellationRequest{SubscriptionId: r.PathValue("id"), Cancel: false})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) changePlan(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ChangePlanRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.ChangePlan(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) previewSubscriptionChange(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ChangeSubscriptionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.PreviewSubscriptionChange(r.Context(), &plansv1.PreviewSubscriptionChangeRequest{Change: req})
	respond(w, r, http.StatusOK, resp.GetQuote(), err)
}

func (s *Server) changeSubscription(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ChangeSubscriptionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.ChangeSubscription(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) cancelScheduledChange(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.CancelScheduledChange(r.Context(), &plansv1.CancelScheduledChangeRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) applyCoupon(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ApplyCouponRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.ApplyCoupon(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) removeCoupon(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.RemoveCoupon(r.Context(), &plansv1.RemoveCouponRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) getTaxRates(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetTaxRates(r.Context(), &plansv1.GetTaxRatesRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) setTaxRates(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetTaxRatesRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.SetTaxRates(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) createCoupon(w http.ResponseWriter, r *http.Request) {
	c := &plansv1.Coupon{}
	if err := decode(r, c); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateCoupon(r.Context(), &plansv1.CreateCouponRequest{Coupon: c})
	respond(w, r, http.StatusCreated, resp.GetCoupon(), err)
}

func (s *Server) listCoupons(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListCoupons(r.Context(), &plansv1.ListCouponsRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) deactivateCoupon(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.DeactivateCoupon(r.Context(), &plansv1.DeactivateCouponRequest{Code: r.PathValue("code")})
	respond(w, r, http.StatusOK, resp.GetCoupon(), err)
}

func (s *Server) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CancelSubscriptionRequest{SubscriptionId: r.PathValue("id")}
	if v := r.URL.Query().Get("end_time"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, r, invalid("end_time", "RFC 3339 timestamp"))
			return
		}
		req.EndTime = timestamppb.New(t)
	}
	switch r.URL.Query().Get("credit_unused") {
	case "", "false":
	case "true":
		req.CreditUnused = true
	default:
		writeError(w, r, invalid("credit_unused", "true or false"))
		return
	}
	resp, err := s.d.Plans.CancelSubscription(r.Context(), req)
	if req.CreditUnused {
		// {subscription, credited_minor}: the caller asked what was credited.
		respond(w, r, http.StatusOK, resp, err)
		return
	}
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateAccountRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Admin.CreateAccount(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetAccount(), err)
}

func (s *Server) getTenant(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.GetTenant(r.Context(), &plansv1.GetTenantRequest{TenantId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetTenant(), err)
}

func (s *Server) updateTenant(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateTenantSettingsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TenantId = r.PathValue("id")
	resp, err := s.d.Admin.UpdateTenantSettings(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetTenant(), err)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.CreateApiKey(r.Context(), &plansv1.CreateApiKeyRequest{TenantId: r.PathValue("id")})
	respond(w, r, http.StatusCreated, resp, err)
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.RevokeApiKey(r.Context(), &plansv1.RevokeApiKeyRequest{ApiKeyId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetApiKey(), err)
}

func (s *Server) getRoutingRules(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetRoutingRules(r.Context(), &plansv1.GetRoutingRulesRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) setRoutingRules(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetRoutingRulesRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.SetRoutingRules(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) previewRouting(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.PreviewRoutingRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.PreviewRouting(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
