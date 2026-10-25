// Package command starts the payment of a live invoice at a provider: a hosted checkout the customer is sent to, or payment instructions (a bank transfer
// reference). The ledger and the attempts record what was started; the provider's notification adds the outcome.
package command

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/invoicepayments/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// LinkKeyPrefix starts the idempotency key of a payment made on a hosted page (a payment link or the customer portal). The provider's confirmed
// notification of such a charge settles the invoice in billing; the ledger row is written first and is idempotent, so a retry after a failed call is safe.
const LinkKeyPrefix = "link:"

// checkoutReuse is how long an opened checkout is handed out again instead of opening a second one. A customer who double-clicks, reloads the page or goes
// back and presses Pay again meets the same provider page, so two payments for one invoice cannot be started by accident.
const checkoutReuse = 30 * time.Minute

// linkNS is the namespace of the command ids of hosted-payment attempts: the key decides the id.
var linkNS = uuid.MustParse("5c1f0e8a-93b7-5d24-b6a1-0f7e2d9c4a38")

// Deps is what the commands need. Query carries the accounts, the resolver and the routing rules.
type Deps struct {
	Query    query.Deps
	Resolver target.Resolver
	Ledger   store.Ledger
	Attempts store.Attempts
	Log      *slog.Logger
}

// Payment is what the hosted page asks to pay.
type Payment struct {
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

// Started is where the customer goes next.
type Started struct {
	// Kind is "redirect" (go to URL) or "instructions" (pay by the Reference and Lines).
	Kind string
	URL  string
	// Reused is set when a checkout that was still open is handed out again.
	Reused    bool
	Reference string
	Lines     []payments.InstructionLine
}

// attemptRequest is what a hosted-payment attempt stores: the webhook reads the amount back from it when the provider does not say.
type attemptRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Method      string `json:"method"`
	SagaID      string `json:"saga_id,omitempty"`
}

// Start begins the payment of an invoice by the method the customer chose.
func Start(ctx context.Context, d Deps, tenant uuid.UUID, in Payment) (Started, error) {
	invoice, err := uuid.Parse(in.InvoiceID)
	in.Currency = strings.ToUpper(in.Currency)
	switch {
	case err != nil:
		return Started{}, refuse.New(http.StatusBadRequest, "payments.invoice.bad_id", "invoice_id must be a UUID")
	case in.AmountMinor <= 0 || in.Currency == "":
		return Started{}, refuse.New(http.StatusBadRequest, "payments.invoice.amount_required", "amount_minor and currency are required")
	case in.Environment != env.Live.String():
		return Started{}, refuse.New(http.StatusConflict, "payments.invoice.not_live", "only live invoices are paid through a provider")
	case in.Method == query.MethodCheckout:
		return startCheckout(ctx, d, tenant, invoice, in)
	case strings.HasPrefix(in.Method, query.MethodInstructions):
		return startInstructions(ctx, d, tenant, invoice, strings.TrimPrefix(in.Method, query.MethodInstructions), in)
	}
	return Started{}, refuse.New(http.StatusBadRequest, "payments.invoice.unknown_method", "unknown method "+in.Method)
}

func startCheckout(ctx context.Context, d Deps, t, invoice uuid.UUID, in Payment) (Started, error) {
	if in.ReturnURL == "" {
		return Started{}, refuse.New(http.StatusBadRequest, "payments.invoice.return_url_required", "return_url is required for a checkout")
	}
	name, err := query.RoutedProvider(ctx, d.Query, t, in.Provider, strings.ToUpper(in.Country), in.Currency)
	if err != nil || name == "" {
		return Started{}, refuse.New(http.StatusConflict, "payments.account.not_found", "the tenant has no provider account for this payment")
	}
	prov, err := liveProvider(ctx, d, t, name)
	if err != nil {
		return Started{}, err
	}
	cp, ok := prov.(payments.InvoiceCheckoutProvider)
	if !ok || !prov.Capabilities().CanCharge(in.Currency) {
		return Started{}, refuse.New(http.StatusConflict, "payments.checkout.currency_unsupported", name+" cannot take a checkout payment in "+in.Currency)
	}
	// A checkout that is still open is handed out again.
	open, found, err := d.Ledger.OpenCheckoutOf(ctx, t, invoice, name, time.Now().Add(-checkoutReuse))
	if err != nil {
		return Started{}, fmt.Errorf("find an open checkout: %w", err)
	}
	if found {
		return Started{Kind: "redirect", URL: open.URL, Reused: true}, nil
	}
	n, err := d.Attempts.CountLinkAttempts(ctx, t, invoice)
	if err != nil {
		return Started{}, fmt.Errorf("count the payment attempts: %w", err)
	}
	key := fmt.Sprintf("%s%s:%d", LinkKeyPrefix, invoice, n+1)
	// The key decides the command id, so two requests racing for the same attempt number are one attempt: the loser does not open a second checkout.
	redelivered, err := startAttempt(ctx, d, t, invoice, name, key, attemptRequest{AmountMinor: in.AmountMinor, Currency: in.Currency, Method: query.MethodCheckout, SagaID: in.SagaID})
	if err != nil {
		return Started{}, err
	}
	if redelivered {
		return Started{}, refuse.New(http.StatusConflict, "payments.invoice.payment_in_progress", "a payment for this invoice is being started; try again in a moment")
	}
	co, err := cp.CreateInvoiceCheckout(ctx, payments.CheckoutInvoiceRequest{IdempotencyKey: key, InvoiceID: invoice.String(), Number: in.Number,
		Description: in.Description, AmountMinor: in.AmountMinor, Currency: in.Currency, CustomerEmail: in.CustomerEmail, CustomerName: in.CustomerName, ReturnURL: in.ReturnURL})
	if err != nil {
		return Started{}, &refuse.Provider{What: "invoice checkout", Err: err}
	}
	// Pending is a fact about money at the provider: the customer was sent there. The notification adds the outcome.
	recordPending(ctx, d, t, invoice, name, key, in, co.Ref, "checkout:"+co.URL)
	return Started{Kind: "redirect", URL: co.URL}, nil
}

func startInstructions(ctx context.Context, d Deps, t, invoice uuid.UUID, name string, in Payment) (Started, error) {
	prov, err := liveProvider(ctx, d, t, name)
	if err != nil {
		return Started{}, err
	}
	ip, ok := prov.(payments.InstructionsProvider)
	if !ok || !prov.Capabilities().CanCharge(in.Currency) {
		return Started{}, refuse.New(http.StatusConflict, "payments.invoice.no_instructions", name+" has no payment instructions for "+in.Currency)
	}
	// One attempt per invoice and provider: the page can be shown again and again and the customer always gets the same reference.
	key := fmt.Sprintf("%s%s:i:%s", LinkKeyPrefix, invoice, name)
	if _, err := startAttempt(ctx, d, t, invoice, name, key, attemptRequest{AmountMinor: in.AmountMinor, Currency: in.Currency, Method: in.Method, SagaID: in.SagaID}); err != nil {
		return Started{}, err
	}
	ins, err := ip.PaymentInstructions(ctx, payments.InstructionsRequest{IdempotencyKey: key, InvoiceID: invoice.String(), Number: in.Number, AmountMinor: in.AmountMinor, Currency: in.Currency})
	if err != nil {
		return Started{}, &refuse.Provider{What: "payment instructions", Err: err}
	}
	recordPending(ctx, d, t, invoice, name, key, in, ins.Reference, "instructions:"+ins.Reference)
	lines := ins.Lines
	if lines == nil {
		lines = []payments.InstructionLine{}
	}
	return Started{Kind: "instructions", Reference: ins.Reference, Lines: lines}, nil
}

// liveProvider is the adapter of the tenant's live account at the provider, or a 409 when it has none.
func liveProvider(ctx context.Context, d Deps, t uuid.UUID, name string) (payments.Provider, error) {
	prov, err := d.Resolver.Named(ctx, t, name, env.Live.String())
	if err != nil {
		return nil, refuse.New(http.StatusConflict, "payments.account.not_found", "no account at "+name+" for this tenant")
	}
	return prov, nil
}

// startAttempt records that the provider is about to be called. The key decides the command id. It reports whether the attempt was already there.
func startAttempt(ctx context.Context, d Deps, t, invoice uuid.UUID, provider, key string, req attemptRequest) (bool, error) {
	redelivered, err := d.Attempts.Start(ctx, uuid.NewSHA1(linkNS, []byte(key)), t, invoice, "charge", env.Live.String(), provider, key, req)
	if err != nil {
		return false, fmt.Errorf("record the payment attempt: %w", err)
	}
	return redelivered, nil
}

// recordPending writes the pending ledger row of a started payment. A failure is logged and not returned: the customer has the provider's page or reference
// already, and the notification writes the outcome.
func recordPending(ctx context.Context, d Deps, t, invoice uuid.UUID, provider, key string, in Payment, ref, detail string) {
	_, err := d.Ledger.Append(ctx, store.LedgerEntry{TenantID: t, InvoiceID: invoice, Provider: provider, Environment: env.Live.String(), Kind: "charge", Status: "pending",
		AmountMinor: in.AmountMinor, Currency: in.Currency, ProviderRef: ref, IdempotencyKey: key, Source: "command", Detail: detail})
	if err != nil {
		d.Log.ErrorContext(ctx, "ledger write failed for a started payment", "provider", provider, "invoice", invoice, "err", err)
	}
}
