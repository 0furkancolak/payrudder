package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
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
		{"POST", "/v1/contract-withdrawals/{id}/refund", AccessTenant, ratelimit.ClassWrite, s.requestContractWithdrawalRefund},
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

// listContractAcceptances is GET /v1/customers/{external_id}/contract-acceptances?kind=&source=&version_id=&from=&to=&page_size=&page_token=: the customer's acceptance log,
// newest first, a page at a time; from is inclusive and to exclusive (RFC 3339).
func (s *Server) listContractAcceptances(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := &plansv1.ListContractAcceptancesRequest{CustomerExternalId: r.PathValue("external_id"), Kind: q.Get("kind"), Source: q.Get("source"), VersionId: q.Get("version_id"),
		PageSize: size, PageToken: token}
	for name, dst := range map[string]**timestamppb.Timestamp{"from": &req.FromTime, "to": &req.ToTime} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, r, invalid(name, "must be an RFC 3339 time"))
				return
			}
			*dst = timestamppb.New(t)
		}
	}
	resp, err := s.d.Contracts.ListContractAcceptances(r.Context(), req)
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

// contractSnapshot is GET /v1/contract-acceptances/{id}/snapshot?pdf=: the page the customer accepted as one self-contained HTML document in `html`, with its sha256; with
// pdf=true also the same page drawn as a PDF (base64 in `pdf`, or `pdf_unavailable` when the page has characters the PDF font cannot draw). The page is the customer's text
// and the seller's variables: whoever shows it shows it as untrusted (the console puts it in a sandboxed frame).
func (s *Server) contractSnapshot(w http.ResponseWriter, r *http.Request) {
	pdf, _ := strconv.ParseBool(r.URL.Query().Get("pdf"))
	resp, err := s.d.Contracts.GetContractSnapshot(r.Context(), &plansv1.GetContractSnapshotRequest{AcceptanceId: r.PathValue("id"), IncludePdf: pdf})
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
	if err != nil {
		respond(w, r, http.StatusOK, resp.GetWithdrawal(), err)
		return
	}
	out := resp.GetWithdrawal()
	// Accepting a withdrawal is the seller's decision to give the money back; the setting says whether Payrudder then asks for the refund too. The decision is already
	// recorded: whatever happens to the refund is written on the withdrawal, never an error that would make the seller decide again.
	if req.GetStatus() == "accepted" && out.GetInvoiceId() != "" {
		if st, err := s.d.Contracts.GetContractSettings(r.Context(), &plansv1.GetContractSettingsRequest{}); err == nil && st.GetSettings().GetWithdrawalRefund() == "full" {
			if refunded, err := s.refundWithdrawal(r, out.GetId(), req.GetRefundAmountMinor()); err == nil {
				out = refunded
			} else {
				s.d.Log.WarnContext(r.Context(), "withdrawal accepted, refund not asked for", "withdrawal", out.GetId(), "error", err)
			}
		}
	}
	respond(w, r, http.StatusOK, out, nil)
}

// requestContractWithdrawalRefund is POST /v1/contract-withdrawals/{id}/refund: ask billing to refund an accepted withdrawal (the settings say none, or the first request
// failed). The refund is a credit note that returns the money to the card; a payment made some other way (a bank transfer) is marked as one to refund by hand.
func (s *Server) requestContractWithdrawalRefund(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.RequestContractWithdrawalRefundRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.refundWithdrawal(r, r.PathValue("id"), req.GetAmountMinor())
	respond(w, r, http.StatusOK, out, err)
}

// refundWithdrawal asks billing for the refund of an accepted withdrawal's invoice through the credit-note path billing already has (a credit note with refund_to_card: only
// a paid invoice, never more than what is left of it, and only a payment a provider can return) and writes the answer on the withdrawal. plans-svc hands the request to one
// caller at a time and never twice; a request left standing by a caller that died is taken over after a few minutes, and then the credit notes of the invoice are searched
// for the one it made before another is made.
func (s *Server) refundWithdrawal(r *http.Request, id string, amount int64) (*plansv1.ContractWithdrawal, error) {
	ctx := r.Context()
	claim, err := s.d.Contracts.ClaimContractWithdrawalRefund(ctx, &plansv1.ClaimContractWithdrawalRefundRequest{Id: id})
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	invoice, reason := claim.GetWithdrawal().GetInvoiceId(), "withdrawal "+id
	record := func(state, creditNote, note string) (*plansv1.ContractWithdrawal, error) {
		rec, err := s.d.Contracts.RecordContractWithdrawalRefund(ctx, &plansv1.RecordContractWithdrawalRefundRequest{Id: id, State: state, CreditNoteId: creditNote, Note: note})
		return rec.GetWithdrawal(), err
	}
	if claim.GetReclaimed() {
		if list, err := s.d.Billing.ListCreditNotes(ctx, &billingv1.ListCreditNotesRequest{InvoiceId: invoice}); err == nil {
			for _, cn := range list.GetCreditNotes() {
				if cn.GetReason() == reason {
					return record("requested", cn.GetId(), "found: made by an earlier request")
				}
			}
		} else {
			return record("failed", "", "billing could not be asked whether an earlier request made a credit note: "+err.Error())
		}
	}
	resp, err := s.d.BillingAdmin.CreateCreditNote(tenant.WithOperator(ctx), &billingv1.CreateCreditNoteRequest{TenantId: ti.ID.String(), InvoiceId: invoice, AmountMinor: amount, Reason: reason, RefundToCard: true})
	switch key := errkey.Of(err); {
	case err == nil:
		return record("requested", resp.GetCreditNote().GetId(), "")
	case key == "billing.credit_note.nothing_to_refund":
		return record("outside", "", "the invoice was not paid by a card payment Payrudder can return (a bank transfer or cash): refund it outside Payrudder, then mark this withdrawal refunded")
	case key == "billing.credit_note.not_paid":
		return record("none", "", "the invoice is not paid: there is nothing to refund")
	case key == "billing.credit_note.fully_credited":
		return record("none", "", "the invoice was credited in full already")
	default:
		return record("failed", "", truncateRunes(err.Error(), 900))
	}
}
