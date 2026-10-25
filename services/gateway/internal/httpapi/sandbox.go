package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The sandbox (phase 45 E): the scenario catalogue and a one-click "simulate" on an invoice, for the tenant API of a sandbox key (or a person acting on their sandbox),
// and the reset of an app's sandbox for people.

func (s *Server) sandboxRoutes() []Route {
	return []Route{
		{"GET", "/v1/sandbox/scenarios", AccessSandbox, ratelimit.ClassRead, s.sandboxScenarios},
		{"POST", "/v1/sandbox/invoices/{id}/simulate", AccessSandbox, ratelimit.ClassWrite, s.sandboxSimulate},
		{"GET", "/app/v1/apps/{id}/sandbox/reset", AccessSession, ratelimit.ClassRead, s.appSandboxResetStatus},
		{"POST", "/app/v1/apps/{id}/sandbox/reset", AccessSession, ratelimit.ClassWrite, s.appSandboxReset},
	}
}

// scenario is one of the payment scenarios of a sandbox customer. The tokens are the pm_sandbox_* payment methods mock-psp understands; what each does is
// docs/03-services/workers.md. Outcome is a stable word the console translates.
type scenario struct {
	Scenario      string `json:"scenario"`
	PaymentMethod string `json:"payment_method"`
	Outcome       string `json:"outcome"`
	Default       bool   `json:"default,omitempty"`
	Description   string `json:"description"`
}

var scenarios = []scenario{
	{"success", "pm_sandbox_success", "paid", true, "The charge succeeds: the invoice is paid."},
	{"declined", "pm_sandbox_declined", "uncollectible", false, "Every attempt is declined (card_declined): dunning retries three times, then the invoice is uncollectible."},
	{"insufficient_funds_once", "pm_sandbox_insufficient_funds_once", "paid_after_retry", false, "The first attempt is declined, the dunning retry succeeds: the invoice is paid."},
	{"error_then_success", "pm_sandbox_error_then_success", "paid_after_retry", false, "The provider answers with a server error twice, then succeeds: retried by the worker, the invoice is paid."},
	{"timeout_committed", "pm_sandbox_timeout_committed", "paid_after_status_query", false, "The provider takes the payment and then does not answer in time: the status query finds it, the invoice is paid."},
	{"timeout_not_committed", "pm_sandbox_timeout_not_committed", "paid_after_recharge", false, "The provider does not answer and has not taken the payment: the status query finds nothing, the charge is made again, the invoice is paid."},
}

func scenarioByName(name string) (scenario, bool) {
	name = strings.TrimPrefix(name, "pm_sandbox_")
	for _, sc := range scenarios {
		if sc.Scenario == name {
			return sc, true
		}
	}
	return scenario{}, false
}

// sandboxScenarios is GET /v1/sandbox/scenarios.
func (s *Server) sandboxScenarios(w http.ResponseWriter, _ *http.Request) {
	writeJSONValue(w, http.StatusOK, map[string]any{"scenarios": scenarios})
}

// sandboxSimulate is POST /v1/sandbox/invoices/{id}/simulate {"scenario": "declined"}: set the invoice's customer to the scenario and run the invoice's payment
// again. The scenario stays on the customer (it is the customer's payment method), so later invoices follow it too. If the invoice's payment cannot be run
// again now (it is paid, or already in progress), the scenario is still set and the answer says the retry was not possible.
func (s *Server) sandboxSimulate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scenario string `json:"scenario"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, invalid("scenario", "required, one of GET /v1/sandbox/scenarios"))
		return
	}
	sc, ok := scenarioByName(body.Scenario)
	if !ok {
		writeError(w, r, errkey.New(codes.InvalidArgument, "billing.sandbox.unknown_scenario", "scenario: one of GET /v1/sandbox/scenarios"))
		return
	}
	inv, err := s.d.Billing.GetInvoice(r.Context(), &billingv1.GetInvoiceRequest{InvoiceId: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	ext := inv.GetInvoice().GetCustomerExternalId()
	if _, err := s.d.Plans.SetPaymentMethod(r.Context(), &plansv1.SetPaymentMethodRequest{ExternalId: ext, PaymentMethod: sc.PaymentMethod}); err != nil {
		writeError(w, r, err)
		return
	}
	out := map[string]any{"invoice_id": inv.GetInvoice().GetId(), "customer_external_id": ext, "scenario": sc.Scenario, "payment_method": sc.PaymentMethod, "retried": false}
	p := principalFrom(r.Context())
	_, err = s.d.BillingAdmin.RetryInvoiceSaga(tenant.WithOperator(r.Context()), &billingv1.RetryInvoiceSagaRequest{TenantId: p.Tenant.ID.String(), InvoiceId: inv.GetInvoice().GetId()})
	if err == nil {
		out["retried"] = true
	} else {
		// The scenario is set; the payment of this invoice is not run again.
		out["retry_error"] = errkey.Of(err)
		if out["retry_error"] == "" {
			out["retry_error"] = status.Code(err).String()
		}
	}
	noStore(w)
	writeJSONValue(w, http.StatusOK, out)
}

// appSandboxReset is POST /app/v1/apps/{id}/sandbox/reset: queue the reset of the app's sandbox (owners and admins). 202: the work is a job; poll the GET.
func (s *Server) appSandboxReset(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.ResetMySandbox(r.Context(), &plansv1.ResetMySandboxRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	respond(w, r, http.StatusAccepted, resp, err)
}

// appSandboxResetStatus is GET /app/v1/apps/{id}/sandbox/reset: the latest reset of the app's sandbox, running or finished (an empty object when there has been none).
func (s *Server) appSandboxResetStatus(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.GetMySandboxReset(r.Context(), &plansv1.GetMySandboxResetRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp, err)
}
