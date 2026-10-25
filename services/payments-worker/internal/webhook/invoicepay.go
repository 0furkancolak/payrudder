package webhook

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// The hosted pages (payment links and the customer portal, phase 34) ask these endpoints which ways a live invoice can be paid and to start one.
// Sandbox and demo invoices are paid with the mock provider through the saga and never reach this code: a sandbox request is refused here, so no
// real provider is called for a sandbox tenant (ADR 0011).

const (
	methodCheckout     = "checkout"
	methodInstructions = "instructions:"
	// checkoutReuse is how long an opened checkout is handed out again instead of opening a second one. A customer who double-clicks, reloads the
	// page or goes back and presses Pay again meets the same provider page, so two payments for one invoice cannot be started by accident.
	checkoutReuse = 30 * time.Minute
	maxLookup     = 200
)

// PaymentMethod is one way to pay an invoice.
type PaymentMethod struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // checkout or instructions
	Provider string `json:"provider"`
}

// routedProvider is the provider an invoice is paid through: the one named, else the one the tenant's routing rules choose for the customer's
// country and the currency, else the tenant's default account.
func (s *Server) routedProvider(ctx context.Context, tenant uuid.UUID, named, country, currency string) (string, error) {
	if named != "" {
		return named, nil
	}
	if s.Plans != nil {
		name, err := s.Plans.PreviewRouting(ctx, tenant, country, currency)
		if err != nil {
			s.Log.WarnContext(ctx, "routing preview failed; using the default account", "tenant", tenant, "err", err)
		} else if name != "" {
			return name, nil
		}
	}
	name, _, err := s.Accounts.Default(ctx, tenant, env.Live.String())
	return name, err
}

// invoiceMethods is GET /admin/tenants/{tenant}/invoice-methods?environment=live&currency=&country=&provider=
func (s *Server) invoiceMethods(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	methods := []PaymentMethod{}
	if q.Get("environment") != env.Live.String() {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"methods": methods})
		return
	}
	ctx := r.Context()
	currency := strings.ToUpper(q.Get("currency"))
	accounts, err := s.Accounts.List(ctx, t)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	routed, err := s.routedProvider(ctx, t, q.Get("provider"), strings.ToUpper(q.Get("country")), currency)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	for _, a := range accounts {
		if a.Environment != env.Live.String() {
			continue
		}
		prov, err := s.Resolver.Named(ctx, t, a.Provider, env.Live.String())
		if err != nil {
			s.Log.WarnContext(ctx, "provider account not usable for payment methods", "tenant", t, "provider", a.Provider, "err", err)
			continue
		}
		if !prov.Capabilities().CanCharge(currency) {
			continue
		}
		if _, ok := prov.(payments.InvoiceCheckoutProvider); ok && a.Provider == routed {
			methods = append(methods, PaymentMethod{ID: methodCheckout, Kind: "checkout", Provider: a.Provider})
		}
		if _, ok := prov.(payments.InstructionsProvider); ok {
			methods = append(methods, PaymentMethod{ID: methodInstructions + a.Provider, Kind: "instructions", Provider: a.Provider})
		}
	}
	// The card checkout comes first, manual ways after it.
	sort.SliceStable(methods, func(i, j int) bool { return methods[i].Kind == "checkout" && methods[j].Kind != "checkout" })
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"methods": methods})
}

type invoicePaymentInput struct {
	InvoiceID     string `json:"invoice_id"`
	SagaID        string `json:"saga_id"`
	Number        string `json:"number"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	Method        string `json:"method"`
	Provider      string `json:"provider"`
	Country       string `json:"country"`
	CustomerEmail string `json:"customer_email"`
	CustomerName  string `json:"customer_name"`
	Description   string `json:"description"`
	ReturnURL     string `json:"return_url"`
	Environment   string `json:"environment"`
}

// attemptRequest is what a hosted-payment attempt stores: the webhook reads the amount back from it when the provider does not say.
type attemptRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Method      string `json:"method"`
	SagaID      string `json:"saga_id,omitempty"`
}

var linkNS = uuid.MustParse("5c1f0e8a-93b7-5d24-b6a1-0f7e2d9c4a38")

// invoicePayment is POST /admin/tenants/{tenant}/invoice-payments.
func (s *Server) invoicePayment(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	var in invoicePaymentInput
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	invoice, err := uuid.Parse(in.InvoiceID)
	in.Currency = strings.ToUpper(in.Currency)
	switch {
	case err != nil:
		httpx.Fail(w, http.StatusBadRequest, "payments.invoice.bad_id", "invoice_id must be a UUID")
	case in.AmountMinor <= 0 || in.Currency == "":
		httpx.Fail(w, http.StatusBadRequest, "payments.invoice.amount_required", "amount_minor and currency are required")
	case in.Environment != "live":
		httpx.Fail(w, http.StatusConflict, "payments.invoice.not_live", "only live invoices are paid through a provider")
	case in.Method == methodCheckout:
		s.startCheckout(w, r, t, invoice, in)
	case strings.HasPrefix(in.Method, methodInstructions):
		s.startInstructions(w, r, t, invoice, strings.TrimPrefix(in.Method, methodInstructions), in)
	default:
		httpx.Fail(w, http.StatusBadRequest, "payments.invoice.unknown_method", "unknown method "+in.Method)
	}
}

func (s *Server) startCheckout(w http.ResponseWriter, r *http.Request, t, invoice uuid.UUID, in invoicePaymentInput) {
	ctx := r.Context()
	if in.ReturnURL == "" {
		httpx.Fail(w, http.StatusBadRequest, "payments.invoice.return_url_required", "return_url is required for a checkout")
		return
	}
	name, err := s.routedProvider(ctx, t, in.Provider, strings.ToUpper(in.Country), in.Currency)
	if err != nil || name == "" {
		httpx.Fail(w, http.StatusConflict, "payments.account.not_found", "the tenant has no provider account for this payment")
		return
	}
	prov, err := s.Resolver.Named(ctx, t, name, env.Live.String())
	if err != nil {
		httpx.Fail(w, http.StatusConflict, "payments.account.not_found", "no account at "+name+" for this tenant")
		return
	}
	cp, ok := prov.(payments.InvoiceCheckoutProvider)
	if !ok || !prov.Capabilities().CanCharge(in.Currency) {
		httpx.Fail(w, http.StatusConflict, "payments.checkout.currency_unsupported", name+" cannot take a checkout payment in "+in.Currency)
		return
	}
	// A checkout that is still open is handed out again.
	if open, found, err := s.Ledger.OpenCheckoutOf(ctx, t, invoice, name, time.Now().Add(-checkoutReuse)); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	} else if found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"kind": "redirect", "url": open.URL, "reused": true})
		return
	}
	n, err := s.Attempts.CountLinkAttempts(ctx, t, invoice)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	key := fmt.Sprintf("%s%s:%d", LinkKeyPrefix, invoice, n+1)
	// The key decides the command id, so two requests racing for the same attempt number are one attempt: the loser does not open a second checkout.
	redelivered, err := s.Attempts.Start(ctx, uuid.NewSHA1(linkNS, []byte(key)), t, invoice, "charge", env.Live.String(), name, key,
		attemptRequest{AmountMinor: in.AmountMinor, Currency: in.Currency, Method: methodCheckout, SagaID: in.SagaID})
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	if redelivered {
		httpx.Fail(w, http.StatusConflict, "payments.invoice.payment_in_progress", "a payment for this invoice is being started; try again in a moment")
		return
	}
	co, err := cp.CreateInvoiceCheckout(ctx, payments.CheckoutInvoiceRequest{IdempotencyKey: key, InvoiceID: invoice.String(), Number: in.Number,
		Description: in.Description, AmountMinor: in.AmountMinor, Currency: in.Currency, CustomerEmail: in.CustomerEmail, CustomerName: in.CustomerName, ReturnURL: in.ReturnURL})
	if err != nil {
		httpx.ProviderFail(s.Log, w, r, "invoice checkout", err)
		return
	}
	// Pending is a fact about money at the provider: the customer was sent there. The notification adds the outcome.
	if _, err := s.Ledger.Append(ctx, store.LedgerEntry{TenantID: t, InvoiceID: invoice, Provider: name, Environment: env.Live.String(), Kind: "charge", Status: "pending",
		AmountMinor: in.AmountMinor, Currency: in.Currency, ProviderRef: co.Ref, IdempotencyKey: key, Source: "command", Detail: "checkout:" + co.URL}); err != nil {
		s.Log.ErrorContext(ctx, "ledger write failed for a checkout", "provider", name, "invoice", invoice, "err", err)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"kind": "redirect", "url": co.URL})
}

func (s *Server) startInstructions(w http.ResponseWriter, r *http.Request, t, invoice uuid.UUID, name string, in invoicePaymentInput) {
	ctx := r.Context()
	prov, err := s.Resolver.Named(ctx, t, name, env.Live.String())
	if err != nil {
		httpx.Fail(w, http.StatusConflict, "payments.account.not_found", "no account at "+name+" for this tenant")
		return
	}
	ip, ok := prov.(payments.InstructionsProvider)
	if !ok || !prov.Capabilities().CanCharge(in.Currency) {
		httpx.Fail(w, http.StatusConflict, "payments.invoice.no_instructions", name+" has no payment instructions for "+in.Currency)
		return
	}
	// One attempt per invoice and provider: the page can be shown again and again and the customer always gets the same reference.
	key := fmt.Sprintf("%s%s:i:%s", LinkKeyPrefix, invoice, name)
	if _, err := s.Attempts.Start(ctx, uuid.NewSHA1(linkNS, []byte(key)), t, invoice, "charge", env.Live.String(), name, key,
		attemptRequest{AmountMinor: in.AmountMinor, Currency: in.Currency, Method: in.Method, SagaID: in.SagaID}); err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	ins, err := ip.PaymentInstructions(ctx, payments.InstructionsRequest{IdempotencyKey: key, InvoiceID: invoice.String(), Number: in.Number, AmountMinor: in.AmountMinor, Currency: in.Currency})
	if err != nil {
		httpx.ProviderFail(s.Log, w, r, "payment instructions", err)
		return
	}
	ref := ins.Reference
	if _, err := s.Ledger.Append(ctx, store.LedgerEntry{TenantID: t, InvoiceID: invoice, Provider: name, Environment: env.Live.String(), Kind: "charge", Status: "pending",
		AmountMinor: in.AmountMinor, Currency: in.Currency, ProviderRef: ref, IdempotencyKey: key, Source: "command", Detail: "instructions:" + ref}); err != nil {
		s.Log.ErrorContext(ctx, "ledger write failed for payment instructions", "provider", name, "invoice", invoice, "err", err)
	}
	lines := ins.Lines
	if lines == nil {
		lines = []payments.InstructionLine{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"kind": "instructions", "reference": ref, "lines": lines})
}
