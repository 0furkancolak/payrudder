package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
)

// The tenant API of payment links and customer portal links (phase 34). The public pages they open are in hosted_*.go and portal.go.

func (s *Server) paymentLinkRoutes() []Route {
	return []Route{
		{"POST", "/v1/payment-links", AccessTenant, ratelimit.ClassWrite, s.createPaymentLink},
		{"GET", "/v1/payment-links", AccessTenant, ratelimit.ClassRead, s.listPaymentLinks},
		{"GET", "/v1/payment-links/{id}", AccessTenant, ratelimit.ClassRead, s.getPaymentLink},
		{"DELETE", "/v1/payment-links/{id}", AccessTenant, ratelimit.ClassWrite, s.revokePaymentLink},
		{"POST", "/v1/customers/{external_id}/portal-links", AccessTenant, ratelimit.ClassWrite, s.createPortalLink},
		{"DELETE", "/v1/customers/{external_id}/portal-links", AccessTenant, ratelimit.ClassWrite, s.revokePortalLinks},
		{"GET", "/v1/customers/{external_id}/payments", AccessTenant, ratelimit.ClassRead, s.listCustomerPayments},
	}
}

// flatJSON renders a message as a JSON object, to add fields that are not part of it.
func flatJSON(msg proto.Message) (map[string]any, error) {
	b, err := marshal.Marshal(msg)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	return m, json.Unmarshal(b, &m)
}

// createPaymentLink is POST /v1/payment-links: 201 with the link and its token, or 200 with the link of the same idempotency key (no token).
func (s *Server) createPaymentLink(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.CreatePaymentLinkRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	resp, err := s.d.Billing.CreatePaymentLink(r.Context(), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := flatJSON(resp.GetLink())
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "encode response"))
		return
	}
	out["token"], out["url"], out["replayed"] = resp.GetToken(), "", resp.GetReplayed()
	code := http.StatusCreated
	if resp.GetToken() != "" {
		out["url"] = s.d.PublicURL + "/i/" + resp.GetToken()
	}
	if resp.GetReplayed() {
		code = http.StatusOK
	}
	writeJSONValue(w, code, out)
}

// listPaymentLinks is GET /v1/payment-links?kind=&status=&invoice_id=&customer=&page_size=&page_token=.
func (s *Server) listPaymentLinks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &billingv1.ListPaymentLinksRequest{Kind: q.Get("kind"), Status: q.Get("status"), InvoiceId: q.Get("invoice_id"), CustomerExternalId: q.Get("customer"), PageToken: q.Get("page_token")}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("page_size", "must be an integer"))
			return
		}
		req.PageSize = int32(n)
	}
	resp, err := s.d.Billing.ListPaymentLinks(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getPaymentLink(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.GetPaymentLink(r.Context(), &billingv1.GetPaymentLinkRequest{LinkId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetLink(), err)
}

func (s *Server) revokePaymentLink(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.RevokePaymentLink(r.Context(), &billingv1.RevokePaymentLinkRequest{LinkId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetLink(), err)
}

// createPortalLink is POST /v1/customers/{external_id}/portal-links.
func (s *Server) createPortalLink(w http.ResponseWriter, r *http.Request) {
	req := &billingv1.CreatePortalLinkRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CustomerExternalId = r.PathValue("external_id")
	resp, err := s.d.Billing.CreatePortalLink(r.Context(), req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusCreated, map[string]any{
		"token": resp.GetToken(), "url": s.d.PublicURL + "/c/" + resp.GetToken(), "expires_at": resp.GetExpireTime().AsTime().UTC().Format(time.RFC3339), "customer_id": resp.GetCustomerId(),
	})
}

func (s *Server) revokePortalLinks(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Billing.RevokePortalLinks(r.Context(), &billingv1.RevokePortalLinksRequest{CustomerExternalId: r.PathValue("external_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"revoked": resp.GetRevoked()})
}

// listCustomerPayments is GET /v1/customers/{external_id}/payments?limit=: the customer's finalized invoices and the ledger rows recorded for them.
func (s *Server) listCustomerPayments(w http.ResponseWriter, r *http.Request) {
	limit := int64(100)
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > 200 {
			writeError(w, r, invalid("limit", "must be an integer from 1 to 200"))
			return
		}
		limit = n
	}
	cust, err := s.d.Plans.ResolveCustomer(r.Context(), &plansv1.ResolveCustomerRequest{ExternalId: r.PathValue("external_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	list, err := s.d.Billing.ListInvoices(r.Context(), &billingv1.ListInvoicesRequest{CustomerId: cust.GetCustomer().GetId(), PageSize: int32(limit)}) //nolint:gosec // 1..200
	if err != nil {
		writeError(w, r, err)
		return
	}
	invoices := []json.RawMessage{}
	numbers := map[string]string{}
	var ids []string
	for _, inv := range list.GetInvoices() {
		if inv.GetStatus() == "draft" {
			continue
		}
		b, err := marshal.Marshal(inv)
		if err != nil {
			writeError(w, r, status.Error(codes.Internal, "encode response"))
			return
		}
		invoices = append(invoices, b)
		numbers[inv.GetId()] = inv.GetNumber()
		ids = append(ids, inv.GetId())
	}
	ti, _ := tenant.FromContext(r.Context())
	rows, err := s.ledgerLookup(r.Context(), ti.ID.String(), ids)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "ledger lookup failed", "error", err)
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.ledger_unreachable", "the payment ledger is not reachable"))
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].OccurredAt.After(rows[j].OccurredAt) })
	payments := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		payments = append(payments, map[string]any{
			"invoice_id": row.InvoiceID, "invoice_number": numbers[row.InvoiceID], "kind": row.Kind, "status": row.Status, "amount_minor": itoa(row.amount()),
			"currency": row.Currency, "provider": row.Provider, "environment": row.Environment, "occurred_at": row.OccurredAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"invoices": invoices, "payments": payments})
}
