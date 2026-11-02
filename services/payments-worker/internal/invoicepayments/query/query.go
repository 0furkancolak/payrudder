// Package query reads the ways a live invoice can be paid. It never writes.
package query

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/buyer"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// The hosted pages (payment links and the customer portal, phase 34) ask which ways a live invoice can be paid and start one. Sandbox and demo invoices are
// paid with the mock provider through the saga and never reach this code: a sandbox request is refused, so no real provider is called for a sandbox tenant
// (ADR 0011).

// Kinds of payment method.
const (
	KindCheckout     = "checkout"
	KindInstructions = "instructions"
	// KindStoredCard is the customer's stored card, authenticated at their bank (3-D Secure).
	KindStoredCard = "stored_card"
)

// Method ids. A method that needs a provider says which in its id.
const (
	MethodCheckout     = KindCheckout
	MethodInstructions = KindInstructions + ":"
	MethodStoredCard   = KindStoredCard
)

// Router names the provider the tenant's routing rules send a customer to.
type Router interface {
	// PreviewRouting names the provider the tenant's routing rules send a customer in a country paying in a currency to; empty when no rule matches.
	PreviewRouting(ctx context.Context, tenant uuid.UUID, country, currency string) (string, error)
}

// Deps is what the queries read. Plans nil means plans-svc is not configured: no routing rules apply.
type Deps struct {
	Accounts store.Accounts
	Resolver target.Resolver
	Plans    Router
	// Customers, when set, lets a customer who has a stored card at a provider that can authenticate it be offered that payment.
	Customers buyer.Reader
	Log       *slog.Logger
}

// InvoiceLink is the provider's own document (invoice or receipt) for a payment it took.
func InvoiceLink(ctx context.Context, api payments.DocumentLinker, transactionRef string) (string, error) {
	url, err := api.InvoiceURL(ctx, transactionRef)
	if err != nil {
		return "", &refuse.Provider{What: "invoice link", Err: err}
	}
	return url, nil
}

// Method is one way to pay an invoice.
type Method struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // checkout or instructions
	Provider string `json:"provider"`
}

// Filter narrows the methods to those that suit a payment.
type Filter struct {
	Environment string
	Currency    string
	Country     string
	// Provider, when set, is the provider the invoice is paid through; otherwise the routing rules and the default account decide.
	Provider string
	// CustomerExternalID, when set, is the customer the invoice is out to.
	CustomerExternalID string
}

// RoutedProvider is the provider an invoice is paid through: the one named, else the one the tenant's routing rules choose for the customer's country and
// the currency, else the tenant's default account.
func RoutedProvider(ctx context.Context, d Deps, tenant uuid.UUID, named, country, currency string) (string, error) {
	if named != "" {
		return named, nil
	}
	if d.Plans != nil {
		name, err := d.Plans.PreviewRouting(ctx, tenant, country, currency)
		if err != nil {
			d.Log.WarnContext(ctx, "routing preview failed; using the default account", "tenant", tenant, "err", err)
		} else if name != "" {
			return name, nil
		}
	}
	name, _, err := d.Accounts.Default(ctx, tenant, env.Live.String())
	return name, err
}

// Methods lists the ways the tenant's live accounts can take the payment: the card checkout of the routed provider, and the manual ways (payment
// instructions) of any account. A sandbox request has none.
func Methods(ctx context.Context, d Deps, tenant uuid.UUID, f Filter) ([]Method, error) {
	methods := []Method{}
	if f.Environment != env.Live.String() {
		return methods, nil
	}
	currency := strings.ToUpper(f.Currency)
	accounts, err := d.Accounts.List(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list provider accounts: %w", err)
	}
	routed, err := RoutedProvider(ctx, d, tenant, f.Provider, strings.ToUpper(f.Country), currency)
	if err != nil {
		return nil, fmt.Errorf("route the payment: %w", err)
	}
	for _, a := range accounts {
		if a.Environment != env.Live.String() {
			continue
		}
		prov, err := d.Resolver.Named(ctx, tenant, a.Provider, env.Live.String())
		if err != nil {
			d.Log.WarnContext(ctx, "provider account not usable for payment methods", "tenant", tenant, "provider", a.Provider, "err", err)
			continue
		}
		if !prov.Capabilities().CanCharge(currency) {
			continue
		}
		if _, ok := prov.(payments.InvoiceCheckoutProvider); ok && a.Provider == routed {
			methods = append(methods, Method{ID: MethodCheckout, Kind: KindCheckout, Provider: a.Provider})
		}
		if tp, ok := prov.(payments.ThreeDSProvider); ok && a.Provider == routed && f.CustomerExternalID != "" && d.Customers != nil {
			// A customer who cannot be read is offered no stored card (the page still has the checkout), and the log says why.
			cu, err := d.Customers.GetCustomer(ctx, tenant, f.CustomerExternalID)
			switch {
			case err != nil && status.Code(err) != codes.NotFound:
				d.Log.WarnContext(ctx, "customer not readable for the stored card method", "tenant", tenant, "err", err)
			case err == nil && tp.StoresPaymentMethod(cu.GetPaymentMethod()):
				methods = append(methods, Method{ID: MethodStoredCard, Kind: KindStoredCard, Provider: a.Provider})
			}
		}
		if _, ok := prov.(payments.InstructionsProvider); ok {
			methods = append(methods, Method{ID: MethodInstructions + a.Provider, Kind: KindInstructions, Provider: a.Provider})
		}
	}
	// The card checkout comes first, the stored card next, manual ways after them.
	rank := func(k string) int {
		switch k {
		case KindCheckout:
			return 0
		case KindStoredCard:
			return 1
		}
		return 2
	}
	sort.SliceStable(methods, func(i, j int) bool { return rank(methods[i].Kind) < rank(methods[j].Kind) })
	return methods, nil
}
