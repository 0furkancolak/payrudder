// Package mailevents is the notifier's inbound side of deliverability: the endpoint a provider's bounce and complaint notices reach (through a small translating
// layer, see mailx.Event) and the operator's view of the suppression list they fill. A permanent bounce or a complaint puts the recipient on the list of the tenant
// the mail was sent for, and the notifier stops mailing that address for that tenant.
//
// All routes are signed with the shared secret like Payrudder's own webhooks (pkg/webhooksig). A GET or DELETE has an empty body, so its signature covers only the
// timestamp: a captured request can be replayed for five minutes, which for a list and for lifting a suppression is harmless.
package mailevents

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

// Notes finds the mail a provider message id belongs to.
type Notes interface {
	ByMessageID(ctx context.Context, messageID string) (tenantID uuid.UUID, recipient string, found bool, err error)
}

// List is the suppression list.
type List interface {
	Add(ctx context.Context, s store.Suppression) (bool, error)
	List(ctx context.Context, tenantID uuid.UUID, after string, pageSize int32) ([]store.Suppression, error)
	Remove(ctx context.Context, tenantID uuid.UUID, address string) (bool, error)
}

// Handler serves the mail-event and suppression routes.
type Handler struct {
	// Secret signs every request; the listener is not started without one.
	Secret string
	Notes  Notes
	List   List
	Log    *slog.Logger
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

const (
	maxBody  = 64 << 10
	maxPage  = 500
	maxAddr  = 320
	outcomes = "outcome"
)

var eventsTotal = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/services/notifier").Int64Counter("notifier_mail_events",
		metric.WithDescription("Delivery events received from the provider, by type and outcome (suppressed, counted, unmatched)"))
	if err != nil {
		panic(err)
	}
	return c
}()

// Routes returns the handler of the three routes.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/mail-events", h.signed(h.event))
	mux.HandleFunc("GET /v1/mail-suppressions", h.signed(h.list))
	mux.HandleFunc("PUT /v1/mail-suppressions/{tenant_id}/{address}", h.signed(h.add))
	mux.HandleFunc("DELETE /v1/mail-suppressions/{tenant_id}/{address}", h.signed(h.remove))
	return mux
}

type problem struct {
	Error string `json:"error"`
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// signed reads the body (at most 64 KiB), checks the signature over it and passes the body on.
func (h *Handler) signed(next func(http.ResponseWriter, *http.Request, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || len(body) > maxBody {
			write(w, http.StatusRequestEntityTooLarge, problem{"the body is too large"})
			return
		}
		now := time.Now
		if h.Now != nil {
			now = h.Now
		}
		if h.Secret == "" || webhooksig.Verify(r.Header.Get(webhooksig.Header()), body, h.Secret, now(), 0) != nil {
			write(w, http.StatusUnauthorized, problem{"the signature is missing or does not verify"})
			return
		}
		next(w, r, body)
	}
}

type eventAnswer struct {
	// Matched: the message id is one the notifier sent.
	Matched bool `json:"matched"`
	// Suppressed: the event put the address on the tenant's list (false when it was there already).
	Suppressed bool `json:"suppressed"`
}

// event takes a neutral delivery event. A hard bounce and a complaint suppress the address the notifier recorded for the message; every event is counted. The
// request is idempotent: a repeat finds the address on the list already.
func (h *Handler) event(w http.ResponseWriter, r *http.Request, body []byte) {
	var e mailx.Event
	if err := json.Unmarshal(body, &e); err != nil {
		write(w, http.StatusBadRequest, problem{"the body is not an event"})
		return
	}
	if err := e.Validate(); err != nil {
		write(w, http.StatusBadRequest, problem{err.Error()})
		return
	}
	ctx := r.Context()
	count := func(outcome string) {
		eventsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("type", string(e.Type)), attribute.String(outcomes, outcome)))
	}
	tenantID, recipient, found, err := h.Notes.ByMessageID(ctx, e.MessageID)
	if err != nil {
		h.Log.ErrorContext(ctx, "mail event: lookup failed", "error", err)
		write(w, http.StatusInternalServerError, problem{"try again"})
		return
	}
	// An event that names another address than the one the mail went to is not about this mail: it must not suppress anything.
	if !found || tenantID == uuid.Nil || (e.Recipient != "" && !strings.EqualFold(strings.TrimSpace(e.Recipient), recipient)) {
		count("unmatched")
		write(w, http.StatusOK, eventAnswer{})
		return
	}
	if !e.Suppresses() {
		count("counted")
		write(w, http.StatusOK, eventAnswer{Matched: true})
		return
	}
	reason := store.ReasonBounce
	if e.Type == mailx.EventComplained {
		reason = store.ReasonComplaint
	}
	added, err := h.List.Add(ctx, store.Suppression{TenantID: tenantID, Address: recipient, Reason: reason, Source: e.MessageID, Detail: clip(e.Detail)})
	if err != nil {
		h.Log.ErrorContext(ctx, "mail event: suppression failed", "error", err)
		write(w, http.StatusInternalServerError, problem{"try again"})
		return
	}
	count("suppressed")
	if added {
		h.Log.InfoContext(ctx, "mail suppressed", "tenant_id", tenantID, "reason", reason, "message_id", e.MessageID)
	}
	write(w, http.StatusOK, eventAnswer{Matched: true, Suppressed: added})
}

func clip(s string) string {
	if len(s) > 500 {
		return strings.ToValidUTF8(s[:500], "")
	}
	return s
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ []byte) {
	tid, err := uuid.Parse(r.URL.Query().Get("tenant_id"))
	if err != nil {
		write(w, http.StatusBadRequest, problem{"tenant_id is required"})
		return
	}
	size := 100
	if v := r.URL.Query().Get("page_size"); v != "" {
		if size, err = strconv.Atoi(v); err != nil || size < 1 || size > maxPage {
			write(w, http.StatusBadRequest, problem{"page_size is 1 to " + strconv.Itoa(maxPage)})
			return
		}
	}
	rows, err := h.List.List(r.Context(), tid, r.URL.Query().Get("after"), int32(size)) //nolint:gosec // 1..500
	if err != nil {
		h.Log.ErrorContext(r.Context(), "mail suppressions: list failed", "error", err)
		write(w, http.StatusInternalServerError, problem{"try again"})
		return
	}
	next := ""
	if len(rows) == size {
		next = rows[len(rows)-1].Address
	}
	write(w, http.StatusOK, map[string]any{"suppressions": rows, "next_after": next})
}

func (h *Handler) target(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	tid, err := uuid.Parse(r.PathValue("tenant_id"))
	addr := strings.TrimSpace(r.PathValue("address"))
	if err != nil || addr == "" || len(addr) > maxAddr || !strings.Contains(addr, "@") {
		write(w, http.StatusBadRequest, problem{"a tenant id and an e-mail address are required"})
		return uuid.Nil, "", false
	}
	return tid, addr, true
}

// add suppresses an address by hand ("do not mail this person"); the body may carry {"detail": "..."}.
func (h *Handler) add(w http.ResponseWriter, r *http.Request, body []byte) {
	tid, addr, ok := h.target(w, r)
	if !ok {
		return
	}
	var in struct {
		Detail string `json:"detail"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			write(w, http.StatusBadRequest, problem{"the body is not JSON"})
			return
		}
	}
	added, err := h.List.Add(r.Context(), store.Suppression{TenantID: tid, Address: addr, Reason: store.ReasonManual, Source: "operator", Detail: clip(in.Detail)})
	if err != nil {
		h.Log.ErrorContext(r.Context(), "mail suppressions: add failed", "error", err)
		write(w, http.StatusInternalServerError, problem{"try again"})
		return
	}
	write(w, http.StatusOK, map[string]bool{"added": added})
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request, _ []byte) {
	tid, addr, ok := h.target(w, r)
	if !ok {
		return
	}
	removed, err := h.List.Remove(r.Context(), tid, addr)
	if err != nil {
		h.Log.ErrorContext(r.Context(), "mail suppressions: remove failed", "error", err)
		write(w, http.StatusInternalServerError, problem{"try again"})
		return
	}
	if !removed {
		write(w, http.StatusNotFound, problem{"the address is not on the list"})
		return
	}
	write(w, http.StatusOK, map[string]bool{"removed": true})
}
