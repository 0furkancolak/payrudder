// Package storedcards lists and deletes the cards a payment provider holds for a customer (iyzico's card storage). The customer's payment method names the provider's
// user key (the record of the customer, never a caller, says whose cards they are), so nobody can read or delete the cards of another customer by naming a key. No
// card number is ever here: the provider answers with the card's BIN, last four digits, bank and expiry. Payrudder cannot put a card in storage (it holds no card
// numbers); it lists them, deletes them, charges them and authenticates a payment with them.
package storedcards

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/buyer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// Handler serves the endpoints.
type Handler struct {
	Accounts  store.Accounts
	Resolver  target.Resolver
	Customers buyer.Reader
	Log       *slog.Logger
}

// Register adds the routes to mux, each wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	const base = "/admin/tenants/{tenant}/customers/{external_id}/cards"
	mux.HandleFunc("GET "+base, guard(h.list))
	mux.HandleFunc("DELETE "+base+"/{token}", guard(h.delete))
}

// card is a stored card as the API shows it.
type card struct {
	Token       string `json:"token"`
	Alias       string `json:"alias"`
	BIN         string `json:"bin"`
	Last4       string `json:"last4"`
	Type        string `json:"type"`
	Association string `json:"association"`
	Family      string `json:"family"`
	Bank        string `json:"bank"`
	ExpireMonth string `json:"expire_month"`
	ExpireYear  string `json:"expire_year"`
	// InUse is set for the card the customer's payment method names: their invoices are charged with it.
	InUse bool `json:"in_use"`
}

// holder is where a customer's cards are: the provider whose card storage the customer's payment method belongs to, and the user key it names.
type holder struct {
	provider string
	store    payments.CardStore
	userKey  string
	method   string
}

// find resolves whose cards the customer's payment method names among the tenant's live accounts. No holder (a customer with no stored card, or a payment method of a
// provider that has no card storage) is not an error: nil.
func (h *Handler) find(ctx context.Context, tenant uuid.UUID, externalID string) (*holder, error) {
	if h.Customers == nil {
		return nil, refuse.New(http.StatusServiceUnavailable, "payments.customer.not_ready", "plans-svc is not configured")
	}
	c, err := h.Customers.GetCustomer(ctx, tenant, externalID)
	if status.Code(err) == codes.NotFound {
		return nil, refuse.New(http.StatusNotFound, "payments.customer.not_ready", "no such customer")
	}
	if err != nil {
		return nil, &refuse.Provider{What: "read the customer", Err: err}
	}
	if c.GetPaymentMethod() == "" {
		return nil, nil
	}
	accounts, err := h.Accounts.List(ctx, tenant)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		if a.Environment != env.Live.String() {
			continue
		}
		prov, err := h.Resolver.Named(ctx, tenant, a.Provider, env.Live.String())
		if err != nil {
			continue
		}
		if cs, ok := prov.(payments.CardStore); ok {
			if key, ok := cs.CardUserKey(c.GetPaymentMethod()); ok {
				return &holder{provider: a.Provider, store: cs, userKey: key, method: c.GetPaymentMethod()}, nil
			}
		}
	}
	return nil, nil
}

func (h *Handler) cards(ctx context.Context, hd *holder) ([]card, error) {
	list, err := hd.store.ListCards(ctx, hd.userKey)
	if err != nil {
		return nil, &refuse.Provider{What: "list the stored cards", Err: err}
	}
	out := make([]card, 0, len(list))
	for _, k := range list {
		out = append(out, card{Token: k.Token, Alias: k.Alias, BIN: k.BIN, Last4: k.Last4, Type: k.Type, Association: k.Association, Family: k.Family, Bank: k.Bank,
			ExpireMonth: k.ExpireMonth, ExpireYear: k.ExpireYear, InUse: strings.HasSuffix(hd.method, ":"+k.Token)})
	}
	return out, nil
}

// list is GET /admin/tenants/{tenant}/customers/{external_id}/cards: the cards the provider holds for the customer, newest answer of the provider, or none.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	hd, err := h.find(r.Context(), t, r.PathValue("external_id"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	if hd == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"provider": "", "cards": []card{}})
		return
	}
	cards, err := h.cards(r.Context(), hd)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"provider": hd.provider, "cards": cards})
}

// delete is DELETE /admin/tenants/{tenant}/customers/{external_id}/cards/{token}: the card must be one of the customer's own (it is looked for in the list the provider
// gives for the customer's user key first, so a token of another customer is a 404 and nothing is sent to delete it). The customer's payment method is not changed: when
// the card was the one in use, the answer says so and the seller sets another.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	hd, err := h.find(r.Context(), t, r.PathValue("external_id"))
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	if hd == nil {
		httpx.Reply(h.Log, w, r, refuse.New(http.StatusNotFound, "payments.card.not_found", "the customer has no such card"))
		return
	}
	cards, err := h.cards(r.Context(), hd)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	var found *card
	for i := range cards {
		if cards[i].Token == r.PathValue("token") {
			found = &cards[i]
		}
	}
	if found == nil {
		httpx.Reply(h.Log, w, r, refuse.New(http.StatusNotFound, "payments.card.not_found", "the customer has no such card"))
		return
	}
	if err := hd.store.DeleteCard(r.Context(), hd.userKey, found.Token); err != nil {
		httpx.Reply(h.Log, w, r, &refuse.Provider{What: "delete the stored card", Err: err})
		return
	}
	h.Log.InfoContext(r.Context(), "a stored card was deleted", "tenant", t, "provider", hd.provider, "customer", r.PathValue("external_id"), "last4", found.Last4, "in_use", found.InUse)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"provider": hd.provider, "deleted": true, "was_in_use": found.InUse})
}
