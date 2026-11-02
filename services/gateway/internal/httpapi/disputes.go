package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
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
	// FeeMinor and FeeCurrency are what the provider took for the case, when it said; Handled is the seller's own mark (the time and who are HandledTime and HandledBy, the
	// latter not for a viewer); NoteCount is how many notes the seller wrote.
	FeeMinor    string     `json:"fee_minor,omitempty"`
	FeeCurrency string     `json:"fee_currency,omitempty"`
	Handled     bool       `json:"handled"`
	HandledTime *time.Time `json:"handled_time,omitempty"`
	HandledBy   string     `json:"handled_by,omitempty"`
	NoteCount   int64      `json:"note_count"`
}

// workerDispute is a case as payments-worker writes it.
type workerDispute struct {
	ID          json.Number  `json:"id"`
	Provider    string       `json:"provider"`
	DisputeID   string       `json:"dispute_id"`
	PaymentRef  string       `json:"payment_ref"`
	InvoiceID   string       `json:"invoice_id"`
	AmountMinor json.Number  `json:"amount_minor"`
	Currency    string       `json:"currency"`
	Outcome     string       `json:"outcome"`
	Formal      bool         `json:"formal"`
	Status      string       `json:"provider_status"`
	Reason      string       `json:"reason"`
	EvidenceDue *time.Time   `json:"evidence_due_time"`
	OpenedTime  time.Time    `json:"opened_time"`
	ClosedTime  *time.Time   `json:"closed_time"`
	FeeMinor    *json.Number `json:"fee_minor"`
	FeeCurrency string       `json:"fee_currency"`
	HandledTime *time.Time   `json:"handled_time"`
	HandledBy   string       `json:"handled_by"`
	NoteCount   int64        `json:"note_count"`
}

// view is the case as the tenant API says it. Who marked it is the seller's colleagues' business: a viewer of the account is not told, as in the audit log.
func (d workerDispute) view(viewer bool) disputeView {
	amount, _ := d.AmountMinor.Int64()
	v := disputeView{ID: d.ID.String(), Provider: d.Provider, DisputeID: d.DisputeID, PaymentRef: d.PaymentRef, InvoiceID: d.InvoiceID, AmountMinor: itoa(amount),
		Currency: d.Currency, Outcome: d.Outcome, Formal: d.Formal, Status: d.Status, Reason: d.Reason, EvidenceDue: d.EvidenceDue, OpenedTime: d.OpenedTime.UTC(), ClosedTime: d.ClosedTime,
		Handled: d.HandledTime != nil, HandledTime: d.HandledTime, NoteCount: d.NoteCount}
	if d.FeeMinor != nil {
		if fee, err := d.FeeMinor.Int64(); err == nil && fee > 0 {
			v.FeeMinor, v.FeeCurrency = itoa(fee), d.FeeCurrency
		}
	}
	if !viewer {
		v.HandledBy = d.HandledBy
	}
	return v
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
		Disputes []workerDispute `json:"disputes"`
	}
	code, err := s.paymentsDo(r.Context(), http.MethodGet, "/admin/tenants/"+url.PathEscape(ti.ID.String())+"/disputes", want, nil, &out)
	if err != nil || code/100 != 2 {
		s.d.Log.WarnContext(r.Context(), "disputes read failed", "status", code, "error", err)
		writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.ledger_unreachable", "the payment ledger is not reachable"))
		return
	}
	viewer := principalFrom(r.Context()).MemberRole == "viewer"
	views := make([]disputeView, 0, len(out.Disputes))
	for _, d := range out.Disputes {
		views = append(views, d.view(viewer))
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"disputes": views})
}

// What a seller does about a case inside Payrudder: read what was written on it, add a note, mark it as handled. Payrudder submits no evidence to a provider (docs/plans/08).

type disputeEventView struct {
	ID      string    `json:"id"`
	Action  string    `json:"action"`
	Note    string    `json:"note,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Created time.Time `json:"create_time"`
}

type workerDisputeEvent struct {
	ID      json.Number `json:"id"`
	Action  string      `json:"action"`
	Note    string      `json:"note"`
	Actor   string      `json:"actor"`
	Created time.Time   `json:"create_time"`
}

func (e workerDisputeEvent) view(viewer bool) disputeEventView {
	v := disputeEventView{ID: e.ID.String(), Action: e.Action, Note: e.Note, Created: e.Created.UTC()}
	if !viewer {
		v.Actor = e.Actor
	}
	return v
}

// disputePath is the worker's path for something about the caller's case, or false after answering 400 for an id that is not the number Payrudder gave a case.
func (s *Server) disputePath(w http.ResponseWriter, r *http.Request, rest string) (string, bool) {
	ti, ok := tenant.FromContext(r.Context())
	if !ok {
		writeError(w, r, errkey.New(codes.Unauthenticated, "identity.tenant.metadata_missing", "no tenant"))
		return "", false
	}
	if n, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err != nil || n < 1 {
		writeError(w, r, invalid("id", "must be the id of a dispute, as GET /v1/disputes gives it"))
		return "", false
	}
	return "/admin/tenants/" + url.PathEscape(ti.ID.String()) + "/disputes/" + r.PathValue("id") + rest, true
}

// relayRefusal answers for a call to payments-worker that did not succeed: a refusal it made on purpose (a 4xx with its key) is the caller's, as it said it; anything else is
// the payment service not answering.
func (s *Server) relayRefusal(w http.ResponseWriter, r *http.Request, code int, body []byte, err error) {
	var env struct {
		Error struct {
			Key     string `json:"key"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err == nil && code/100 == 4 && json.Unmarshal(body, &env) == nil && env.Error.Key != "" {
		c := map[int]codes.Code{http.StatusBadRequest: codes.InvalidArgument, http.StatusNotFound: codes.NotFound, http.StatusConflict: codes.FailedPrecondition}[code]
		if c != codes.OK {
			writeError(w, r, errkey.New(c, env.Error.Key, env.Error.Message))
			return
		}
	}
	s.d.Log.WarnContext(r.Context(), "dispute call failed", "status", code, "error", err)
	writeError(w, r, errkey.New(codes.Unavailable, "gateway.payments.ledger_unreachable", "the payment ledger is not reachable"))
}

// listDisputeHistory is GET /v1/disputes/{id}/history.
func (s *Server) listDisputeHistory(w http.ResponseWriter, r *http.Request) {
	path, ok := s.disputePath(w, r, "/history")
	if !ok {
		return
	}
	var out struct {
		Events []workerDisputeEvent `json:"events"`
	}
	if code, body, err := s.paymentsDoBody(r.Context(), http.MethodGet, path, nil, nil, &out); err != nil || code/100 != 2 {
		s.relayRefusal(w, r, code, body, err)
		return
	}
	viewer := principalFrom(r.Context()).MemberRole == "viewer"
	events := make([]disputeEventView, 0, len(out.Events))
	for _, e := range out.Events {
		events = append(events, e.view(viewer))
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"events": events})
}

// addDisputeNote is POST /v1/disputes/{id}/notes {"note"}.
func (s *Server) addDisputeNote(w http.ResponseWriter, r *http.Request) {
	path, ok := s.disputePath(w, r, "/notes")
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := decodeBody(r, &in, invalid("body", "must be JSON like {\"note\": \"...\"}")); err != nil {
		writeError(w, r, err)
		return
	}
	var out workerDisputeEvent
	if code, body, err := s.paymentsDoBody(r.Context(), http.MethodPost, path, nil, in, &out); err != nil || code/100 != 2 {
		s.relayRefusal(w, r, code, body, err)
		return
	}
	writeJSONValue(w, http.StatusCreated, out.view(false))
}

// setDisputeHandled is PUT /v1/disputes/{id}/handled {"handled"}: the case as it stands.
func (s *Server) setDisputeHandled(w http.ResponseWriter, r *http.Request) {
	path, ok := s.disputePath(w, r, "/handled")
	if !ok {
		return
	}
	var in struct {
		Handled *bool `json:"handled"`
	}
	if err := decodeBody(r, &in, invalid("body", "must be JSON like {\"handled\": true}")); err != nil {
		writeError(w, r, err)
		return
	}
	if in.Handled == nil {
		writeError(w, r, invalid("handled", "is required: true or false"))
		return
	}
	var out workerDispute
	if code, body, err := s.paymentsDoBody(r.Context(), http.MethodPut, path, nil, in, &out); err != nil || code/100 != 2 {
		s.relayRefusal(w, r, code, body, err)
		return
	}
	writeJSONValue(w, http.StatusOK, out.view(false))
}
