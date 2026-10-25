// Package invoicepayments serves the hosted pages (payment links and the customer portal, phase 34): which ways a live invoice can be paid, and starting one.
// It maps requests to the commands and queries of its subpackages (ADR 0020).
package invoicepayments

import (
	"log/slog"
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/invoicepayments/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/invoicepayments/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Handler serves the invoice payment endpoints.
type Handler struct {
	Query   query.Deps
	Command command.Deps
	Log     *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /admin/tenants/{tenant}/invoice-methods", guard(h.methods))
	mux.HandleFunc("POST /admin/tenants/{tenant}/invoice-payments", guard(h.start))
	mux.HandleFunc("GET /admin/tenants/{tenant}/providers/{provider}/transactions/{ref}/invoice", guard(h.invoiceLink))
}

// invoiceLink is the provider's own document for a payment it took.
func (h *Handler) invoiceLink(w http.ResponseWriter, r *http.Request) {
	tg, ok := httpx.ProviderTarget(w, r, h.Query.Resolver)
	if !ok {
		return
	}
	api, err := target.As[payments.DocumentLinker](tg, "invoice documents")
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	url, err := query.InvoiceLink(r.Context(), api, r.PathValue("ref"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}

// methods is GET /admin/tenants/{tenant}/invoice-methods?environment=live&currency=&country=&provider=
func (h *Handler) methods(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	list, err := query.Methods(r.Context(), h.Query, t, query.Filter{Environment: q.Get("environment"), Currency: q.Get("currency"), Country: q.Get("country"), Provider: q.Get("provider")})
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"methods": list})
}

// start is POST /admin/tenants/{tenant}/invoice-payments.
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in command.Payment
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	res, err := command.Start(r.Context(), h.Command, t, in)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	switch {
	case res.Kind == "instructions":
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"kind": res.Kind, "reference": res.Reference, "lines": res.Lines})
	case res.Reused:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"kind": res.Kind, "url": res.URL, "reused": true})
	default:
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"kind": res.Kind, "url": res.URL})
	}
}
