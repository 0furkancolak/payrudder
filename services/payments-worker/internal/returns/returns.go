// Package returns finishes a payment made on a page of the provider's when the payer comes back from it (iyzico's checkout form, the bank's 3-D Secure page): the
// provider posts a form to a callback URL of ours through the payer's browser, the gateway hands it here, and the provider is asked what the payment is before anything
// is written. The browser's form is never believed; it only names the payment (payments.ReturnConfirmer). The provider's own notification arrives later and finds the
// ledger row already there (one fact once), so the two are one payment.
package returns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	invoicepayments "github.com/0furkancolak/payrudder/services/payments-worker/internal/invoicepayments/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// States of the outcome the payer is told.
const (
	StatePaid     = "paid"
	StateDeclined = "declined"
	// StatePending: the provider holds the payment (fraud review) or could not be asked yet; the notification settles it.
	StatePending = "pending"
)

// Outcome is what the payer's page says.
type Outcome struct {
	State       string `json:"state"`
	DeclineCode string `json:"decline_code,omitempty"`
}

// Attempts finds the hosted attempt a callback is about, by its key.
type Attempts interface {
	ChargeByKey(ctx context.Context, key string) (store.Attempt, bool, error)
}

// Ledger is the part of the payment ledger a return reads and writes.
type Ledger interface {
	ChargeFact(ctx context.Context, tenant uuid.UUID, key string) (store.Fact, bool, error)
	PendingRef(ctx context.Context, tenant uuid.UUID, key string) (string, bool, error)
	Append(ctx context.Context, e store.LedgerEntry) (bool, error)
}

// Billing settles an invoice whose payment a provider confirmed.
type Billing interface {
	SettleInvoicePayment(ctx context.Context, tenant uuid.UUID, req *billingv1.SettleInvoicePaymentRequest) (*billingv1.SettleInvoicePaymentResponse, error)
}

// Deps is what a return needs. Billing nil means billing-svc is not configured: the ledger is written and the invoice is left to the reconciliation.
type Deps struct {
	Attempts Attempts
	Ledger   Ledger
	Resolver target.Resolver
	Billing  Billing
	Log      *slog.Logger
}

// Complete finishes the hosted payment of key with the form the provider posted. An error is a refusal (httpx.Reply): 404 for an attempt that does not exist, 401 for a
// form that is not the attempt's, 503 when the provider could not say (the payer is told the payment is being processed, and the notification settles it).
func Complete(ctx context.Context, d Deps, key string, form url.Values) (Outcome, error) {
	if !strings.HasPrefix(key, invoicepayments.LinkKeyPrefix) {
		return Outcome{}, refuse.New(http.StatusNotFound, "payments.webhook.not_found", "no such payment")
	}
	at, found, err := d.Attempts.ChargeByKey(ctx, key)
	if err != nil {
		return Outcome{}, err
	}
	if !found {
		return Outcome{}, refuse.New(http.StatusNotFound, "payments.webhook.not_found", "no such payment")
	}
	// A payment that is already settled is answered from the ledger, not asked of the provider again: the payer reloaded the posted page, or the notification came first.
	if f, ok, err := d.Ledger.ChargeFact(ctx, at.TenantID, key); err != nil {
		return Outcome{}, err
	} else if ok {
		if f.Status == store.StatusSucceeded {
			return Outcome{State: StatePaid}, d.settle(ctx, at, f.ProviderRef, false)
		}
		return Outcome{State: StateDeclined, DeclineCode: f.Detail}, nil
	}
	prov, err := d.Resolver.Named(ctx, at.TenantID, at.Provider, at.Environment)
	if err != nil {
		return Outcome{}, refuse.New(http.StatusServiceUnavailable, "payments.webhook.try_again", "try again")
	}
	rc, ok := prov.(payments.ReturnConfirmer)
	if !ok {
		return Outcome{}, refuse.New(http.StatusNotImplemented, "payments.provider.not_supported", at.Provider+" does not support returns")
	}
	ref, _, err := d.Ledger.PendingRef(ctx, at.TenantID, key)
	if err != nil {
		return Outcome{}, err
	}
	st, err := rc.ConfirmReturn(ctx, payments.ReturnRequest{IdempotencyKey: key, InvoiceID: at.InvoiceID.String(), AmountMinor: at.AmountMinor, Currency: at.Currency, ProviderRef: ref, Form: form, PaidMinor: at.PaidMinor})
	var pending *payments.PendingError
	switch {
	case errors.Is(err, payments.ErrUnverified):
		d.Log.WarnContext(ctx, "a return failed verification", "provider", at.Provider, "tenant", at.TenantID, "err", err)
		return Outcome{}, refuse.New(http.StatusUnauthorized, "payments.webhook.unverified", "verification failed")
	case errors.As(err, &pending):
		return Outcome{State: StatePending}, nil
	case err != nil:
		d.Log.WarnContext(ctx, "a return could not be confirmed", "provider", at.Provider, "tenant", at.TenantID, "err", err)
		return Outcome{}, refuse.New(http.StatusServiceUnavailable, "payments.webhook.try_again", "try again")
	}
	// The same row the provider's notification writes (the adapter's name, the charge's key and the provider's id), so whichever arrives second changes nothing.
	entry := store.LedgerEntry{TenantID: at.TenantID, InvoiceID: at.InvoiceID, Provider: payments.BaseProvider(at.Provider), Environment: at.Environment, Kind: store.KindCharge,
		AmountMinor: at.AmountMinor, Currency: at.Currency, ProviderRef: st.ChargeID, IdempotencyKey: key, Source: store.SourceCommand}
	switch st.State {
	case payments.StateSucceeded:
		entry.Status = store.StatusSucceeded
	case payments.StateDeclined:
		entry.Status, entry.Detail = store.StatusDeclined, st.DeclineCode
	default:
		return Outcome{}, refuse.New(http.StatusServiceUnavailable, "payments.webhook.try_again", "try again")
	}
	fresh, err := d.Ledger.Append(ctx, entry)
	if err != nil {
		return Outcome{}, err
	}
	if st.State == payments.StateDeclined {
		return Outcome{State: StateDeclined, DeclineCode: st.DeclineCode}, nil
	}
	return Outcome{State: StatePaid}, d.settle(ctx, at, st.ChargeID, fresh)
}

// settle tells billing that the hosted payment succeeded, as the notification does; billing ignores a payment it has been told about already.
func (d Deps) settle(ctx context.Context, at store.Attempt, providerRef string, fresh bool) error {
	if d.Billing == nil {
		d.Log.WarnContext(ctx, "hosted payment confirmed but billing-svc is not configured", "tenant", at.TenantID, "invoice", at.InvoiceID)
		return nil
	}
	res, err := d.Billing.SettleInvoicePayment(ctx, at.TenantID, &billingv1.SettleInvoicePaymentRequest{
		InvoiceId: at.InvoiceID.String(), Provider: payments.BaseProvider(at.Provider), ProviderRef: providerRef, AmountMinor: at.AmountMinor, Currency: at.Currency})
	if err != nil {
		return fmt.Errorf("settle invoice %s: %w", at.InvoiceID, err)
	}
	if !res.GetSettled() && fresh {
		d.Log.ErrorContext(ctx, "hosted payment for an invoice that was not waiting for one", "provider", at.Provider, "tenant", at.TenantID, "invoice", at.InvoiceID, "ref", providerRef)
	}
	return nil
}

// Handler serves the return endpoint to the gateway.
type Handler struct {
	Deps Deps
	Log  *slog.Logger
}

// Register adds the route to mux, wrapped by guard (the operator token check).
func (h *Handler) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /admin/invoice-payments/return", guard(h.complete))
}

// complete is POST /admin/invoice-payments/return {"key": "...", "form": {"token": ["..."]}}: the form is what the provider posted to the callback URL.
func (h *Handler) complete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key  string     `json:"key"`
		Form url.Values `json:"form"`
	}
	if !httpx.DecodeBody(w, r, &in) {
		return
	}
	out, err := Complete(r.Context(), h.Deps, in.Key, in.Form)
	if err != nil {
		httpx.Reply(h.Log, w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
