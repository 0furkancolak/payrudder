package httpapi

import (
	"net/http"
	"strings"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The agreements a seller presents to its own customers, the evidence that a customer accepted a specific text, and the withdrawal requests (ContractService,
// docs/plans/21-contracts.md). Managing the texts and settings needs contracts:read / contracts:write; recording an acceptance or a withdrawal is a customer record
// (customers:write), so a server that only runs a checkout can do it without being able to change a text.

func (s *Server) contractRoutes() []Route {
	return []Route{
		{"GET", "/v1/contracts/versions", AccessTenant, ratelimit.ClassRead, s.listContractVersions},
		{"POST", "/v1/contracts/versions", AccessTenant, ratelimit.ClassWrite, s.publishContractVersion},
		{"GET", "/v1/contracts/versions/{id}", AccessTenant, ratelimit.ClassRead, s.getContractVersion},
		{"POST", "/v1/contracts/preview", AccessTenant, ratelimit.ClassRead, s.previewContract},
		{"GET", "/v1/contracts/starter", AccessTenant, ratelimit.ClassRead, s.getContractStarter},
		{"GET", "/v1/contracts/settings", AccessTenant, ratelimit.ClassRead, s.getContractSettings},
		{"PUT", "/v1/contracts/settings", AccessTenant, ratelimit.ClassWrite, s.setContractSettings},
		{"GET", "/v1/customers/{external_id}/contract-requirements", AccessTenant, ratelimit.ClassRead, s.contractRequirements},
		{"GET", "/v1/customers/{external_id}/contract-acceptances", AccessTenant, ratelimit.ClassRead, s.listContractAcceptances},
		{"POST", "/v1/customers/{external_id}/contract-acceptances", AccessTenant, ratelimit.ClassWrite, s.recordContractAcceptance},
		{"GET", "/v1/contract-acceptances/{id}/snapshot", AccessTenant, ratelimit.ClassRead, s.contractSnapshot},
		{"POST", "/v1/customers/{external_id}/contract-withdrawals", AccessTenant, ratelimit.ClassWrite, s.createContractWithdrawal},
		{"GET", "/v1/contract-withdrawals", AccessTenant, ratelimit.ClassRead, s.listContractWithdrawals},
		{"PUT", "/v1/contract-withdrawals/{id}/status", AccessTenant, ratelimit.ClassWrite, s.setContractWithdrawalStatus},
	}
}

// contractAuthor is who is recorded as the author of a version or the decider of a withdrawal: the person the console says (X-Payrudder-Actor), else the key.
func contractAuthor(r *http.Request) string {
	if a := strings.TrimSpace(r.Header.Get("X-Payrudder-Actor")); a != "" {
		return truncateRunes(a, 200)
	}
	if ti, ok := tenant.FromContext(r.Context()); ok && ti.APIKeyID != "" {
		return "key " + ti.APIKeyID
	}
	return ""
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (s *Server) listContractVersions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.Contracts.ListContractVersions(r.Context(), &plansv1.ListContractVersionsRequest{Kind: q.Get("kind"), Audience: q.Get("audience")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) publishContractVersion(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.PublishContractVersionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Author = contractAuthor(r)
	resp, err := s.d.Contracts.PublishContractVersion(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetVersion(), err)
}

func (s *Server) getContractVersion(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Contracts.GetContractVersion(r.Context(), &plansv1.GetContractVersionRequest{Id: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetVersion(), err)
}

func (s *Server) previewContract(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.PreviewContractRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Contracts.PreviewContract(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getContractStarter(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.Contracts.GetContractStarter(r.Context(), &plansv1.GetContractStarterRequest{Kind: q.Get("kind"), Locale: q.Get("locale"), Audience: q.Get("audience")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getContractSettings(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Contracts.GetContractSettings(r.Context(), &plansv1.GetContractSettingsRequest{})
	respond(w, r, http.StatusOK, resp.GetSettings(), err)
}

func (s *Server) setContractSettings(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetContractSettingsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Contracts.SetContractSettings(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetSettings(), err)
}

// contractRequirements is GET /v1/customers/{external_id}/contract-requirements: what the customer must accept before a subscription starts, with the text to show
// (an embedded checkout draws its own boxes from it). The order (description, total, currency) fills the variables of the texts.
func (s *Server) contractRequirements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.Contracts.GetContractRequirements(r.Context(), &plansv1.GetContractRequirementsRequest{CustomerExternalId: r.PathValue("external_id"), Locale: q.Get("locale"),
		CustomerType: q.Get("customer_type"), Order: &plansv1.ContractOrder{Description: q.Get("order_description"), Total: q.Get("order_total"), Currency: q.Get("order_currency")}})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) listContractAcceptances(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Contracts.ListContractAcceptances(r.Context(), &plansv1.ListContractAcceptancesRequest{CustomerExternalId: r.PathValue("external_id")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// recordContractAcceptance is POST /v1/customers/{external_id}/contract-acceptances: the seller's server reports that the customer accepted the current versions. The
// source is the API whatever the body says: what a seller reports about the customer's address and browser is its claim, and the record says so.
func (s *Server) recordContractAcceptance(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.RecordContractAcceptanceRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CustomerExternalId, req.CustomerId, req.Source, req.ContextKind = r.PathValue("external_id"), "", "api", "api"
	resp, err := s.d.Contracts.RecordContractAcceptance(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

// contractSnapshot is GET /v1/contract-acceptances/{id}/snapshot: the page the customer accepted as one self-contained HTML document in `html`, with its sha256. The
// page is the customer's text and the seller's variables: whoever shows it shows it as untrusted (the console puts it in a sandboxed frame).
func (s *Server) contractSnapshot(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Contracts.GetContractSnapshot(r.Context(), &plansv1.GetContractSnapshotRequest{AcceptanceId: r.PathValue("id")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

// createContractWithdrawal is POST /v1/customers/{external_id}/contract-withdrawals: the seller records a withdrawal statement that reached it. The source is the API,
// or the console when the console says so; "hosted" belongs to Payrudder's own pages.
func (s *Server) createContractWithdrawal(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateContractWithdrawalRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CustomerExternalId = r.PathValue("external_id")
	if req.GetSource() != "console" {
		req.Source = "api"
	}
	resp, err := s.d.Contracts.CreateContractWithdrawal(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetWithdrawal(), err)
}

func (s *Server) listContractWithdrawals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resp, err := s.d.Contracts.ListContractWithdrawals(r.Context(), &plansv1.ListContractWithdrawalsRequest{CustomerExternalId: q.Get("customer_external_id"), Status: q.Get("status")})
	noStore(w)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) setContractWithdrawalStatus(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetContractWithdrawalStatusRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Id, req.DecidedBy = r.PathValue("id"), contractAuthor(r)
	resp, err := s.d.Contracts.SetContractWithdrawalStatus(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetWithdrawal(), err)
}
