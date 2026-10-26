package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

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
	n, err := intParam(r.URL.Query(), "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.PageSize = n
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

// setCustomerAddress is PUT /v1/customers/{external_id}/address: the body is the address.
func (s *Server) setCustomerAddress(w http.ResponseWriter, r *http.Request) {
	addr := &plansv1.Address{}
	if err := decode(r, addr); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.SetCustomerAddress(r.Context(), &plansv1.SetCustomerAddressRequest{ExternalId: r.PathValue("external_id"), Address: addr})
	respond(w, r, http.StatusOK, resp.GetCustomer(), err)
}

// setPaymentScenario is the sandbox spelling of SetPaymentMethod: {"scenario": "declined"} → pm_sandbox_declined.
func (s *Server) setPaymentScenario(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scenario string `json:"scenario"`
	}
	bad := invalid("scenario", "required, e.g. \"declined\" or \"timeout_committed\"")
	if err := decodeBody(r, &body, bad); err != nil {
		writeError(w, r, err)
		return
	}
	if body.Scenario == "" {
		writeError(w, r, bad)
		return
	}
	resp, err := s.d.Plans.SetPaymentMethod(r.Context(), &plansv1.SetPaymentMethodRequest{
		ExternalId: r.PathValue("external_id"), PaymentMethod: "pm_sandbox_" + body.Scenario,
	})
	respond(w, r, http.StatusOK, resp.GetCustomer(), err)
}
