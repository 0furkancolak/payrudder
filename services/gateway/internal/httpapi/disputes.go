package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// GET /v1/disputes lists the cases customers opened with their banks about the tenant's payments (payments-worker's table of disputes), newest first. The tenant is the
// caller's own; there is no way to name another.

type disputeView struct {
	ID          string     `json:"id"`
	Provider    string     `json:"provider"`
	DisputeID   string     `json:"dispute_id"`
	PaymentRef  string     `json:"payment_ref"`
	InvoiceID   string     `json:"invoice_id,omitempty"`
	AmountMinor string     `json:"amount_minor"`
	Currency    string     `json:"currency"`
	Outcome     string     `json:"outcome"`
	Formal      bool       `json:"formal"`
	Status      string     `json:"provider_status"`
	Reason      string     `json:"reason,omitempty"`
	EvidenceDue *time.Time `json:"evidence_due_time,omitempty"`
	OpenedTime  time.Time  `json:"opened_time"`
	ClosedTime  *time.Time `json:"closed_time,omitempty"`
}

func (s *Server) listDisputes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := int32(100)
	if n, err := rangedParam(q, "limit", 1, 200, "must be an integer from 1 to 200"); err != nil {
		writeError(w, r, err)
		return
	} else if n != 0 {
		limit = n
	}
	if v := q.Get("invoice_id"); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			writeError(w, r, invalid("invoice_id", "must be a UUID"))
			return
		}
	}
	if v := q.Get("outcome"); v != "" && v != "open" && v != "won" && v != "lost" {
		writeError(w, r, invalid("outcome", "must be open, won or lost"))
		return
	}
	ti, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, r, errkey.New(codes.Unauthenticated, "identity.tenant.metadata_missing", "no tenant"))
		return
	}
	want := url.Values{"limit": {itoa(int64(limit))}}
	for _, k := range []string{"invoice_id", "outcome"} {
		if v := q.Get(k); v != "" {
			want.Set(k, v)
		}
	}
	var out struct {
		Disputes []struct {
			ID          json.Number `json:"id"`
			Provider    string      `json:"provider"`
			DisputeID   string      `json:"dispute_id"`
			PaymentRef  string      `json:"payment_ref"`
			InvoiceID   string      `json:"invoice_id"`
			AmountMinor json.Number `json:"amount_minor"`
			Currency    string      `json:"currency"`
			Outcome     string      `json:"outcome"`
			Formal      bool        `json:"formal"`
			Status      string      `json:"provider_status"`
			Reason      string      `json:"reason"`
			EvidenceDue *time.Time  `json:"evidence_due_time"`
			OpenedTime  time.Time   `json:"opened_time"`
			ClosedTime  *time.Time  `json:"closed_time"`
		} `json:"disputes"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(ti.ID.String())+"/disputes", want, nil, &out)
	if err != nil || code/100 != 2 {
		s.d.Log.WarnContext(r.Context(), "disputes read failed", "status", code, "error", err)
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.ledger_unreachable", "the payment ledger is not reachable"))
		return
	}
	views := make([]disputeView, 0, len(out.Disputes))
	for _, d := range out.Disputes {
		amount, _ := d.AmountMinor.Int64()
		views = append(views, disputeView{ID: d.ID.String(), Provider: d.Provider, DisputeID: d.DisputeID, PaymentRef: d.PaymentRef, InvoiceID: d.InvoiceID, AmountMinor: itoa(amount),
			Currency: d.Currency, Outcome: d.Outcome, Formal: d.Formal, Status: d.Status, Reason: d.Reason, EvidenceDue: d.EvidenceDue, OpenedTime: d.OpenedTime.UTC(), ClosedTime: d.ClosedTime})
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"disputes": views})
}
