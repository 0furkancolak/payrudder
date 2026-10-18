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
		{"DELETE", "/v1/subscriptions/{id}", AccessTenant, ratelimit.ClassWrite, s.cancelSubscription},
		// Sandbox
		{"POST", "/v1/sandbox/customers/{external_id}/payment-scenario", AccessSandbox, ratelimit.ClassWrite, s.setPaymentScenario},
		// Operator
		{"POST", "/admin/v1/accounts", AccessOperator, "", s.createAccount},
		{"GET", "/admin/v1/tenants/{id}", AccessOperator, "", s.getTenant},
		{"PATCH", "/admin/v1/tenants/{id}", AccessOperator, "", s.updateTenant},
		{"POST", "/admin/v1/tenants/{id}/api-keys", AccessOperator, "", s.createAPIKey},
		{"DELETE", "/admin/v1/api-keys/{id}", AccessOperator, "", s.revokeAPIKey},
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
	resp, err := s.d.Plans.CancelSubscription(r.Context(), req)
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
