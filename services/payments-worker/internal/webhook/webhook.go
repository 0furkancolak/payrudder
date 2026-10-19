// Package webhook is payments-worker's HTTP side: provider notifications, and the operator's view of provider accounts and the
// ledger. The gateway forwards to it; nothing here is exposed on its own.
//
// A notification is never believed. Its body names a charge; the provider is then asked what that charge is (payments.Notifier), and
// only that answer reaches the ledger.
package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/handler"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
)

// Server serves /webhooks/{provider} and the operator endpoints under /admin.
type Server struct {
	Registry *payments.Registry
	Resolver *handler.Resolver
	Accounts store.Accounts
	Events   store.Events
	Attempts store.Attempts
	Ledger   store.Ledger
	Log      *slog.Logger
	// Token guards /admin. Empty disables the admin endpoints entirely.
	Token string
}

const maxBody = 64 << 10

var (
	meter       = otel.Meter("github.com/0furkancolak/quotify/services/payments-worker/webhook")
	events      = counter("provider_events", "Provider notifications handled, by provider and result")
	discrepancy = counter("payment_discrepancies", "Charges whose recorded outcomes disagree")
)

func counter(name, desc string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(desc))
	if err != nil {
		panic(err)
	}
	return c
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/{provider}", s.notification)
	mux.HandleFunc("GET /admin/tenants/{tenant}/provider-accounts", s.admin(s.listAccounts))
	mux.HandleFunc("PUT /admin/tenants/{tenant}/provider-accounts/{provider}", s.admin(s.putAccount))
	mux.HandleFunc("DELETE /admin/tenants/{tenant}/provider-accounts/{provider}", s.admin(s.deleteAccount))
	mux.HandleFunc("GET /admin/tenants/{tenant}/ledger", s.admin(s.ledger))
	return mux
}

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(s.Token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) result(ctx context.Context, provider, result string) {
	events.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", provider), attribute.String("result", result)))
}

// notification handles a provider webhook. It answers 200 for everything it understands, also for what it ignores, so the provider
// stops retrying; it answers 5xx when it could not confirm with the provider, so the provider tries again.
func (s *Server) notification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.PathValue("provider")
	parse, ok := s.Registry.Parser(name)
	if !ok {
		fail(w, http.StatusNotFound, "unknown provider")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	ref, err := parse(body, r.Header)
	if err != nil {
		s.result(ctx, name, "malformed")
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// The inbox keeps the body as received (JSON), so a body that is not JSON is wrapped rather than dropped.
	stored := body
	if !json.Valid(body) {
		stored, _ = json.Marshal(map[string]string{"raw": string(body)})
	}
	fresh, err := s.Events.Receive(ctx, name, ref.EventID, stored)
	if err != nil {
		s.Log.ErrorContext(ctx, "webhook inbox write failed", "provider", name, "err", err)
		fail(w, http.StatusInternalServerError, "try again")
		return
	}
	if !fresh {
		s.result(ctx, name, "duplicate")
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	finish := func(tenant uuid.UUID, status, msg string) {
		if err := s.Events.Finish(ctx, name, ref.EventID, tenant, status, msg); err != nil {
			s.Log.ErrorContext(ctx, "webhook inbox update failed", "provider", name, "event", ref.EventID, "err", err)
		}
	}
	retry := func(tenant uuid.UUID, err error) {
		// Not confirmed: forget the notification so the provider's retry is handled afresh, not taken for a duplicate.
		s.Log.WarnContext(ctx, "webhook not confirmed", "provider", name, "event", ref.EventID, "err", err)
		finish(tenant, "failed", err.Error())
		if err := s.Events.Forget(ctx, name, ref.EventID); err != nil {
			s.Log.ErrorContext(ctx, "webhook inbox cleanup failed", "provider", name, "event", ref.EventID, "err", err)
		}
		s.result(ctx, name, "retry")
		fail(w, http.StatusServiceUnavailable, "try again")
	}

	at, found, err := s.Attempts.ChargeByKey(ctx, ref.IdempotencyKey)
	if err != nil {
		retry(uuid.Nil, err)
		return
	}
	if !found {
		finish(uuid.Nil, "ignored", "no charge with this reference")
		s.result(ctx, name, "unknown_charge")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	prov, err := s.Resolver.Named(ctx, at.TenantID, name, at.Environment)
	if err != nil {
		retry(at.TenantID, err)
		return
	}
	n, ok := prov.(payments.Notifier)
	if !ok {
		finish(at.TenantID, "ignored", "provider cannot confirm notifications")
		s.result(ctx, name, "unsupported")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	conf, err := n.ConfirmNotification(ctx, body, r.Header)
	if err != nil {
		retry(at.TenantID, err)
		return
	}
	entry := store.LedgerEntry{TenantID: at.TenantID, InvoiceID: at.InvoiceID, Provider: name, Environment: at.Environment, Kind: "charge",
		AmountMinor: at.AmountMinor, Currency: at.Currency, ProviderRef: conf.ProviderRef, IdempotencyKey: ref.IdempotencyKey, Source: "webhook"}
	switch conf.State {
	case payments.StateSucceeded:
		entry.Status = "succeeded"
	case payments.StateDeclined:
		entry.Status, entry.Detail = "declined", conf.DeclineCode
	default:
		retry(at.TenantID, errors.New("provider did not report a final state"))
		return
	}
	if _, err := s.Ledger.Append(ctx, entry); err != nil {
		retry(at.TenantID, err)
		return
	}
	if conf.ProviderRef != "" {
		if n, err := s.Ledger.Outcomes(ctx, name, "charge", conf.ProviderRef); err == nil && n > 1 {
			discrepancy.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", name)))
			s.Log.ErrorContext(ctx, "payment outcomes disagree", "provider", name, "ref", conf.ProviderRef, "tenant", at.TenantID, "invoice", at.InvoiceID)
		}
	}
	finish(at.TenantID, "processed", "")
	s.result(ctx, name, "processed")
	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

func (s *Server) tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	t, err := uuid.Parse(r.PathValue("tenant"))
	if err != nil {
		fail(w, http.StatusBadRequest, "tenant must be a UUID")
		return uuid.Nil, false
	}
	return t, true
}

func environment(w http.ResponseWriter, r *http.Request) (string, bool) {
	env := r.URL.Query().Get("environment")
	if env != "sandbox" && env != "live" {
		fail(w, http.StatusBadRequest, "environment must be sandbox or live")
		return "", false
	}
	return env, true
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	list, err := s.Accounts.List(r.Context(), t)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": list})
}

// putAccount stores (or replaces) an account. The settings are validated by building the adapter, and are never echoed back.
func (s *Server) putAccount(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	env, ok := environment(w, r)
	if !ok {
		return
	}
	provider := r.PathValue("provider")
	settings, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	if _, err := s.Registry.Build(provider, settings); err != nil {
		if errors.Is(err, payments.ErrUnknownProvider) {
			fail(w, http.StatusNotFound, "unknown provider; known: "+join(s.Registry.Names()))
			return
		}
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	makeDefault, _ := strconv.ParseBool(r.URL.Query().Get("default"))
	if err := s.Accounts.Put(r.Context(), t, provider, env, settings, makeDefault); err != nil {
		s.Log.ErrorContext(r.Context(), "provider account write failed", "err", err)
		fail(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider, "environment": env, "default": makeDefault})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	env, ok := environment(w, r)
	if !ok {
		return
	}
	deleted, err := s.Accounts.Delete(r.Context(), t, r.PathValue("provider"), env)
	switch {
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal error")
	case !deleted:
		fail(w, http.StatusNotFound, "no such account")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) ledger(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			fail(w, http.StatusBadRequest, "limit must be 1..1000")
			return
		}
		limit = n
	}
	rows, err := s.Ledger.List(r.Context(), t, limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": rows})
}

func join(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
