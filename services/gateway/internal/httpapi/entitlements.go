package httpapi

import (
	"net/http"
	"time"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

type entitlementJSON struct {
	CustomerExternalID string  `json:"customer_external_id"`
	MeterCode          string  `json:"meter_code"`
	PeriodID           string  `json:"period_id"`
	Used               string  `json:"used"`
	Limit              *string `json:"limit"`
	Unlimited          bool    `json:"unlimited"`
	Allowed            bool    `json:"allowed"`
	AsOf               *string `json:"as_of"`
	Window             string  `json:"window"`
	WindowID           string  `json:"window_id"`
	ResetsAt           *string `json:"resets_at"`
	Mode               string  `json:"mode"`
	OverLimit          bool    `json:"over_limit"`
	Overridden         bool    `json:"overridden"`
}

func entitlementOf(external string, s *limitsv1.LimitStatus) entitlementJSON {
	out := entitlementJSON{
		CustomerExternalID: external, MeterCode: s.GetMeterCode(), PeriodID: s.GetPeriodId(), Used: s.GetUsed(),
		Unlimited: s.GetUnlimited(), Allowed: s.GetAllowed(), Window: s.GetWindow(), WindowID: s.GetWindowId(), Mode: s.GetMode(),
		OverLimit: s.GetOverLimit(), Overridden: s.GetOverridden(),
	}
	if s.GetResetsAt() != nil {
		t := s.GetResetsAt().AsTime().UTC().Format(time.RFC3339Nano)
		out.ResetsAt = &t
	}
	if !s.GetUnlimited() {
		l := s.GetLimit()
		out.Limit = &l
	}
	if s.GetAsOf() != nil {
		t := s.GetAsOf().AsTime().UTC().Format(time.RFC3339Nano)
		out.AsOf = &t
	}
	return out
}

// periodQuery validates the optional ?period= parameter; empty means the tenant's current period.
func periodQuery(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.URL.Query().Get("period")
	if p != "" && !period.Valid(p) {
		writeError(w, r, invalid("period", "must look like 2026-11"))
		return "", false
	}
	return p, true
}

// getEntitlement is GET /v1/customers/{external_id}/entitlements/{meter}: usage against the limit, from the
// near-real-time counters (eventually consistent: as_of tells how fresh they are).
func (s *Server) getEntitlement(w http.ResponseWriter, r *http.Request) {
	periodID, ok := periodQuery(w, r)
	if !ok {
		return
	}
	cust, ok := s.resolveCustomer(w, r, r.PathValue("external_id"))
	if !ok {
		return
	}
	resp, err := s.d.Limits.CheckEntitlement(r.Context(), &limitsv1.CheckEntitlementRequest{
		CustomerId: cust.GetId(), MeterCode: r.PathValue("meter"), PeriodId: periodID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, entitlementOf(cust.GetExternalId(), resp.GetStatus()))
}

// listEntitlements is GET /v1/customers/{external_id}/entitlements: every meter the customer has a limit on.
func (s *Server) listEntitlements(w http.ResponseWriter, r *http.Request) {
	periodID, ok := periodQuery(w, r)
	if !ok {
		return
	}
	cust, ok := s.resolveCustomer(w, r, r.PathValue("external_id"))
	if !ok {
		return
	}
	resp, err := s.d.Limits.GetLimitStatus(r.Context(), &limitsv1.GetLimitStatusRequest{
		CustomerId: cust.GetId(), PeriodId: periodID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	items := make([]entitlementJSON, 0, len(resp.GetStatuses()))
	for _, st := range resp.GetStatuses() {
		items = append(items, entitlementOf(cust.GetExternalId(), st))
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"items": items})
}

// listEntitlementOverrides is GET /v1/customers/{external_id}/entitlement-overrides: the customer's own limits, in place of its plan's.
func (s *Server) listEntitlementOverrides(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListEntitlementOverrides(r.Context(), &plansv1.ListEntitlementOverridesRequest{CustomerExternalId: r.PathValue("external_id")})
	respond(w, r, http.StatusOK, resp, err)
}

// setEntitlementOverride is PUT /v1/customers/{external_id}/entitlement-overrides/{meter}: creates or replaces the customer's limit on the meter.
func (s *Server) setEntitlementOverride(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetEntitlementOverrideRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CustomerExternalId, req.MeterCode = r.PathValue("external_id"), r.PathValue("meter")
	resp, err := s.d.Plans.SetEntitlementOverride(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetOverride(), err)
}

// deleteEntitlementOverride is DELETE /v1/customers/{external_id}/entitlement-overrides/{meter}: back to the plan's limit.
func (s *Server) deleteEntitlementOverride(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Plans.DeleteEntitlementOverride(r.Context(), &plansv1.DeleteEntitlementOverrideRequest{CustomerExternalId: r.PathValue("external_id"), MeterCode: r.PathValue("meter")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
