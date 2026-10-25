// Package webhook is payments-worker's HTTP side: provider notifications, and the operator's view of provider accounts and the
// ledger. The gateway forwards to it; nothing here is exposed on its own.
//
// A notification is never believed. Its body names a charge; the provider is then asked what that charge is (payments.Notifier), and
// only that answer reaches the ledger.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts"
	accountscommand "github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/command"
	accountsquery "github.com/0furkancolak/payrudder/services/payments-worker/internal/accounts/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoiceaccounts"
	einvoicecommand "github.com/0furkancolak/payrudder/services/payments-worker/internal/einvoiceaccounts/command"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/httpx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/ledger"
	ledgercommand "github.com/0furkancolak/payrudder/services/payments-worker/internal/ledger/command"
	ledgerquery "github.com/0furkancolak/payrudder/services/payments-worker/internal/ledger/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation"
	reconciliationquery "github.com/0furkancolak/payrudder/services/payments-worker/internal/reconciliation/query"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// Server serves /webhooks/{provider} and the operator endpoints under /admin.
type Server struct {
	Registry      *payments.Registry
	Resolver      *handler.Resolver
	Accounts      store.Accounts
	Events        store.Events
	Attempts      store.Attempts
	Ledger        store.Ledger
	Subscriptions store.Subscriptions
	// Objects remembers what was created at a provider (products, prices, customers, discounts).
	Objects store.Objects
	Log     *slog.Logger
	// Token guards /admin. Empty disables the admin endpoints entirely.
	Token string
	// EInvoiceAccounts and EInvoiceRegistry serve the e-invoice account endpoints (phase 30); AllowMockLive lets a live account be the mock (demo mode).
	EInvoiceAccounts store.EInvoiceAccounts
	EInvoiceRegistry *einvoice.Registry
	AllowMockLive    bool
	// Billing, when set, lets the reconciliation read the tenant's invoices.
	Billing BillingAPI
	// Plans, when set, is told about subscriptions a provider runs, so plans-svc can link them to ours and follow their status, and runs
	// provider migrations.
	Plans PlansAPI
	// Reset deletes a sandbox tenant's attempts, ledger and receipts (POST /admin/tenants/{tenant}/sandbox-reset); nil answers 503.
	Reset ledgercommand.ResetFunc
	// Modules are the operator endpoints of other modules (the bank transfers), served next to these.
	Modules []RouteRegistrar
}

// PlansAPI is what payments-worker asks of plans-svc, as the tenant.
type PlansAPI interface {
	SyncProviderSubscription(ctx context.Context, tenant uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest) error
	GetSubscription(ctx context.Context, tenant uuid.UUID, id string) (*plansv1.Subscription, error)
	StartProviderMigration(ctx context.Context, tenant uuid.UUID, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error)
	CompleteProviderMigration(ctx context.Context, tenant uuid.UUID, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error)
	AbortProviderMigration(ctx context.Context, tenant uuid.UUID, id string) error
	GetPlanVersion(ctx context.Context, tenant uuid.UUID, req *plansv1.GetPlanVersionRequest) (*plansv1.PlanVersion, error)
	GetCustomer(ctx context.Context, tenant uuid.UUID, externalID string) (*plansv1.Customer, error)
	TenantCurrency(ctx context.Context, tenant uuid.UUID) (string, error)
	// PreviewRouting names the provider the tenant's routing rules send a customer in a country paying in a currency to; empty when no rule matches.
	PreviewRouting(ctx context.Context, tenant uuid.UUID, country, currency string) (string, error)
	// SetBankTransfer replaces the bank details printed on the tenant's invoices; nil clears them.
	SetBankTransfer(ctx context.Context, tenant uuid.UUID, d *plansv1.BankTransferDetails) error
}

// RouteRegistrar adds routes to the server's mux, each behind the operator check.
type RouteRegistrar interface {
	Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc)
}

var (
	meter       = otel.Meter("github.com/0furkancolak/payrudder/services/payments-worker/webhook")
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

// Handler returns the HTTP handler: the provider notifications, and the operator endpoints of the modules.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhooks/{provider}", s.notification)
	mux.HandleFunc("POST /webhooks/{provider}/{tenant}/{environment}", s.tenantNotification)
	guard := httpx.Guard(s.Token)
	for _, m := range s.modules() {
		m.Register(mux, guard)
	}
	return mux
}

// modules are the operator endpoints, one module each, and the modules the caller added.
func (s *Server) modules() []RouteRegistrar {
	mods := []RouteRegistrar{
		&accounts.Handler{Log: s.Log,
			Command: accountscommand.Deps{Registry: s.Registry, Accounts: s.Accounts, Plans: s.Plans, Log: s.Log},
			Query:   accountsquery.Deps{Registry: s.Registry, Accounts: s.Accounts, Log: s.Log}},
		&einvoiceaccounts.Handler{Log: s.Log, Deps: einvoicecommand.Deps{Accounts: s.EInvoiceAccounts, Registry: s.EInvoiceRegistry, AllowMockLive: s.AllowMockLive}},
		&ledger.Handler{Log: s.Log, Query: ledgerquery.Deps{Ledger: s.Ledger}, Command: ledgercommand.Deps{Reset: s.Reset}},
		&reconciliation.Handler{Log: s.Log, Query: reconciliationquery.Deps{Ledger: s.Ledger, Billing: s.Billing}},
		routes{s},
	}
	return append(mods, s.Modules...)
}

// routes are the endpoints that have not moved into a module yet.
type routes struct{ s *Server }

func (r routes) Register(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc) {
	s := r.s
	mux.HandleFunc("GET /admin/tenants/{tenant}/provider-subscriptions", guard(s.listSubscriptions))
	mux.HandleFunc("POST /admin/tenants/{tenant}/checkouts", guard(s.checkout))
	mux.HandleFunc("POST /admin/tenants/{tenant}/migrations", guard(s.migrate))
	mux.HandleFunc("POST /admin/tenants/{tenant}/migrations/{migration}/abort", guard(s.abortMigration))
	mux.HandleFunc("GET /admin/tenants/{tenant}/invoice-methods", guard(s.invoiceMethods))
	mux.HandleFunc("POST /admin/tenants/{tenant}/invoice-payments", guard(s.invoicePayment))
	s.apiRoutes(mux, guard)
}

func (s *Server) result(ctx context.Context, provider, result string) {
	events.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", provider), attribute.String("result", result)))
}

// notification handles a webhook of a provider that names no tenant in its URL (iyzico): the charge it is about is found by the
// idempotency key in the body, and that charge's tenant and environment decide whose account confirms it.
func (s *Server) notification(w http.ResponseWriter, r *http.Request) {
	s.receive(w, r, r.PathValue("provider"), uuid.Nil, "")
}

// tenantNotification handles a webhook whose URL names the tenant and environment (Paddle: one notification destination per account).
func (s *Server) tenantNotification(w http.ResponseWriter, r *http.Request) {
	t, err := uuid.Parse(r.PathValue("tenant"))
	mode, perr := env.Parse(r.PathValue("environment"))
	if err != nil || perr != nil {
		httpx.Fail(w, http.StatusNotFound, "payments.webhook.not_found", "no such webhook")
		return
	}
	s.receive(w, r, r.PathValue("provider"), t, mode.String())
}

// receive is the shared path of a notification. With tenant set the URL decided whose account it is; otherwise the body's charge does.
func (s *Server) receive(w http.ResponseWriter, r *http.Request, name string, tenantID uuid.UUID, env string) {
	ctx := r.Context()
	parse, ok := s.Registry.Parser(name)
	if !ok {
		httpx.Fail(w, http.StatusNotFound, "payments.provider.unknown", "unknown provider")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, httpx.MaxBody))
	if err != nil {
		httpx.Fail(w, http.StatusRequestEntityTooLarge, "payments.request.too_large", "body too large")
		return
	}
	ref, err := parse(body, r.Header)
	if err != nil {
		s.result(ctx, name, "malformed")
		httpx.Fail(w, http.StatusBadRequest, "payments.webhook.malformed", err.Error())
		return
	}
	fresh, err := s.Events.Receive(ctx, name, ref.EventID, inboxBody(body))
	if err != nil {
		s.Log.ErrorContext(ctx, "webhook inbox write failed", "provider", name, "err", err)
		httpx.Fail(w, http.StatusInternalServerError, "payments.webhook.not_recorded", "try again")
		return
	}
	if !fresh {
		s.result(ctx, name, "duplicate")
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	d := &delivery{s: s, w: w, r: r, name: name, eventID: ref.EventID, tenant: tenantID, env: env}
	d.process(body, ref.IdempotencyKey)
}

// inboxBody is the body as the inbox keeps it (JSON): a body that is not JSON is wrapped rather than dropped.
func inboxBody(body []byte) []byte {
	if json.Valid(body) {
		return body
	}
	wrapped, _ := json.Marshal(map[string]string{"raw": string(body)})
	return wrapped
}

// delivery is one received notification on its way to a ledger row, a subscription row, or an answer to the provider.
type delivery struct {
	s       *Server
	w       http.ResponseWriter
	r       *http.Request
	name    string
	eventID string
	tenant  uuid.UUID
	env     string
	at      store.Attempt
}

func (d *delivery) finish(tenant uuid.UUID, status, msg string) {
	if err := d.s.Events.Finish(d.r.Context(), d.name, d.eventID, tenant, status, msg); err != nil {
		d.s.Log.ErrorContext(d.r.Context(), "webhook inbox update failed", "provider", d.name, "event", d.eventID, "err", err)
	}
}

// retry answers 503 and forgets the notification, so the provider's retry is handled afresh, not taken for a duplicate.
func (d *delivery) retry(tenant uuid.UUID, err error) {
	ctx := d.r.Context()
	d.s.Log.WarnContext(ctx, "webhook not confirmed", "provider", d.name, "event", d.eventID, "err", err)
	d.finish(tenant, "failed", err.Error())
	if err := d.s.Events.Forget(ctx, d.name, d.eventID); err != nil {
		d.s.Log.ErrorContext(ctx, "webhook inbox cleanup failed", "provider", d.name, "event", d.eventID, "err", err)
	}
	d.s.result(ctx, d.name, "retry")
	httpx.Fail(d.w, http.StatusServiceUnavailable, "payments.webhook.try_again", "try again")
}

func (d *delivery) ignore(tenant uuid.UUID, why string) {
	d.finish(tenant, "ignored", why)
	d.s.result(d.r.Context(), d.name, "ignored")
	httpx.WriteJSON(d.w, http.StatusOK, map[string]string{"status": "ignored"})
}

func (d *delivery) process(body []byte, key string) {
	ctx := d.r.Context()
	if d.tenant == uuid.Nil && !d.findCharge(key) {
		return
	}
	conf, ok := d.confirm(body)
	if !ok || !d.apply(conf) {
		return
	}
	d.finish(d.tenant, "processed", "")
	d.s.result(ctx, d.name, "processed")
	httpx.WriteJSON(d.w, http.StatusOK, map[string]string{"status": "processed"})
}

// findCharge decides whose account confirms a notification whose URL names no tenant: the tenant and environment of the charge
// its reference points at. It reports false once it has answered.
func (d *delivery) findCharge(key string) bool {
	at, found, err := d.s.Attempts.ChargeByKey(d.r.Context(), key)
	if err != nil {
		d.retry(uuid.Nil, err)
		return false
	}
	if !found {
		d.ignore(uuid.Nil, "no charge with this reference")
		return false
	}
	d.at, d.tenant, d.env = at, at.TenantID, at.Environment
	return true
}

// confirm asks the provider's own account whether the notification is genuine. It reports false once it has answered.
func (d *delivery) confirm(body []byte) (payments.Notification, bool) {
	ctx := d.r.Context()
	prov, err := d.s.Resolver.Named(ctx, d.tenant, d.name, d.env)
	if err != nil {
		d.retry(d.tenant, err)
		return payments.Notification{}, false
	}
	n, ok := prov.(payments.Notifier)
	if !ok {
		d.ignore(d.tenant, "provider cannot confirm notifications")
		return payments.Notification{}, false
	}
	conf, err := n.ConfirmNotification(ctx, body, d.r.Header)
	if errors.Is(err, payments.ErrUnverified) {
		// Not from the provider: nothing to retry. Forget it so a genuine event with the same id is not taken for a duplicate.
		d.s.Log.WarnContext(ctx, "webhook failed verification", "provider", d.name, "tenant", d.tenant, "err", err)
		_ = d.s.Events.Forget(ctx, d.name, d.eventID)
		d.s.result(ctx, d.name, "unverified")
		httpx.Fail(d.w, http.StatusUnauthorized, "payments.webhook.unverified", "verification failed")
		return payments.Notification{}, false
	}
	if err != nil {
		d.retry(d.tenant, err)
		return payments.Notification{}, false
	}
	return conf, true
}

// apply records what the notification says. It reports false once it has answered.
func (d *delivery) apply(conf payments.Notification) bool {
	switch conf.Kind {
	case payments.KindIgnored:
		d.ignore(d.tenant, "event not used")
		return false
	case payments.KindSubscription:
		if err := d.s.recordSubscription(d.r.Context(), d.tenant, d.name, d.env, conf); err != nil {
			d.retry(d.tenant, err)
			return false
		}
	default:
		if err := d.recordCharge(conf); err != nil {
			d.retry(d.tenant, err)
			return false
		}
	}
	return true
}

func (d *delivery) recordCharge(conf payments.Notification) error {
	ctx := d.r.Context()
	entry, err := d.ledgerEntry(conf)
	if err != nil {
		return err
	}
	fresh, err := d.s.Ledger.Append(ctx, entry)
	if err != nil {
		return err
	}
	if conf.ProviderRef != "" {
		if n, err := d.s.Ledger.Outcomes(ctx, entry.TenantID, d.name, entry.Kind, conf.ProviderRef); err == nil && n > 1 {
			discrepancy.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", d.name)))
			d.s.Log.ErrorContext(ctx, "payment outcomes disagree", "provider", d.name, "ref", conf.ProviderRef, "tenant", d.tenant, "invoice", d.at.InvoiceID)
		}
	}
	return d.settleHostedPayment(ctx, entry, fresh)
}

// LinkKeyPrefix starts the idempotency key of a payment made on a hosted page (a payment link or the customer portal). The provider's confirmed
// notification of such a charge settles the invoice in billing; the ledger row is written first and is idempotent, so a retry after a failed call is safe.
const LinkKeyPrefix = "link:"

// settleHostedPayment tells billing that a hosted payment succeeded. An error makes the provider retry the notification.
func (d *delivery) settleHostedPayment(ctx context.Context, entry store.LedgerEntry, fresh bool) error {
	if entry.Kind != "charge" || entry.Status != "succeeded" || !strings.HasPrefix(entry.IdempotencyKey, LinkKeyPrefix) || entry.InvoiceID == uuid.Nil {
		return nil
	}
	if d.s.Billing == nil {
		d.s.Log.WarnContext(ctx, "hosted payment confirmed but billing-svc is not configured", "tenant", d.tenant, "invoice", entry.InvoiceID)
		return nil
	}
	res, err := d.s.Billing.SettleInvoicePayment(ctx, d.tenant, &billingv1.SettleInvoicePaymentRequest{
		InvoiceId: entry.InvoiceID.String(), Provider: d.name, ProviderRef: entry.ProviderRef, AmountMinor: entry.AmountMinor, Currency: entry.Currency})
	if err != nil {
		return fmt.Errorf("settle invoice %s: %w", entry.InvoiceID, err)
	}
	if !res.GetSettled() && fresh {
		// A new confirmed payment for an invoice that is no longer waiting for one (paid another way meanwhile): the customer paid twice. The operator refunds.
		discrepancy.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", d.name)))
		d.s.Log.ErrorContext(ctx, "hosted payment for an invoice that was not waiting for one", "provider", d.name, "tenant", d.tenant, "invoice", entry.InvoiceID, "ref", entry.ProviderRef)
	}
	return nil
}

func (d *delivery) ledgerEntry(conf payments.Notification) (store.LedgerEntry, error) {
	kind := "charge"
	if conf.Kind == payments.KindRefund {
		kind = "refund"
	}
	entry := store.LedgerEntry{TenantID: d.tenant, Provider: d.name, Environment: d.env, Kind: kind, AmountMinor: conf.AmountMinor, Currency: conf.Currency,
		ProviderRef: conf.ProviderRef, IdempotencyKey: conf.Ref.IdempotencyKey, Source: "webhook"}
	if d.at.InvoiceID != uuid.Nil {
		entry.InvoiceID = d.at.InvoiceID
		if entry.AmountMinor == 0 {
			entry.AmountMinor, entry.Currency = d.at.AmountMinor, d.at.Currency
		}
	} else if conf.Ref.IdempotencyKey != "" {
		// A charge of ours the provider settled later (a notification found by its key rather than by the URL).
		if a, found, err := d.s.Attempts.ChargeByKey(d.r.Context(), conf.Ref.IdempotencyKey); err == nil && found && a.TenantID == d.tenant {
			entry.InvoiceID = a.InvoiceID
			if entry.AmountMinor == 0 {
				entry.AmountMinor, entry.Currency = a.AmountMinor, a.Currency
			}
		}
	}
	switch conf.State {
	case payments.StateSucceeded:
		entry.Status = "succeeded"
	case payments.StateDeclined:
		entry.Status, entry.Detail = "declined", conf.DeclineCode
		if kind == "refund" {
			entry.Status = "failed"
		}
	default:
		return entry, errors.New("provider did not report a final state")
	}
	if entry.Currency == "" {
		entry.Currency = "XXX" // the provider did not say; the ledger requires a code
	}
	return entry, nil
}

func (s *Server) recordSubscription(ctx context.Context, tenant uuid.UUID, provider, env string, n payments.Notification) error {
	ev := n.Subscription
	if ev == nil {
		return errors.New("subscription notification without a subscription")
	}
	var q *uuid.UUID
	if id, err := uuid.Parse(ev.PayrudderSubscriptionID); err == nil {
		q = &id
	}
	occurred := n.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now()
	}
	prov, err := s.Resolver.Named(ctx, tenant, provider, env)
	if err != nil {
		return err
	}
	owner := string(prov.Capabilities().Owner())

	// A subscription that completes a provider migration: the cutover happens now, in plans-svc, and the old one is released.
	if mig := ev.PayrudderMigrationID; mig != "" && s.Plans != nil {
		done, err := s.Plans.CompleteProviderMigration(ctx, tenant, &plansv1.CompleteProviderMigrationRequest{
			MigrationId: mig, ProviderRef: ev.ProviderRef, CutoverTime: timestamppb.New(occurred)})
		if err != nil {
			return err
		}
		if id, err := uuid.Parse(done.GetSubscription().GetId()); err == nil {
			q = &id
		}
		if err := s.Subscriptions.Upsert(ctx, tenant, provider, env, ev.ProviderRef, q, ev.Status, ev.NextBilledAt, occurred); err != nil {
			return err
		}
		return s.releaseSource(ctx, tenant, env, done)
	}

	if err := s.Subscriptions.Upsert(ctx, tenant, provider, env, ev.ProviderRef, q, ev.Status, ev.NextBilledAt, occurred); err != nil {
		return err
	}
	if s.Plans == nil {
		return nil
	}
	if q == nil {
		// Later events of a subscription we already know carry no id of ours; the row remembers it.
		known, found, err := s.Subscriptions.PayrudderID(ctx, tenant, provider, env, ev.ProviderRef)
		if err != nil {
			return err
		}
		if !found {
			return nil // a subscription we did not start: kept for the operator, nothing to link
		}
		q = &known
	}
	req := &plansv1.SyncProviderSubscriptionRequest{
		SubscriptionId: q.String(), Provider: provider, ProviderRef: ev.ProviderRef, Owner: owner, ProviderStatus: ev.Status, OccurredAt: timestamppb.New(occurred),
	}
	// What the provider bills the subscription for: if it is a price Payrudder created, the plan version and interval follow.
	if pv, iv, ok := s.catalogueTarget(ctx, tenant, provider, env, ev.PriceRefs); ok {
		req.PlanVersionId, req.Interval = pv, iv
	}
	return s.Plans.SyncProviderSubscription(ctx, tenant, req)
}

// releaseSource stops the old provider from billing a subscription that has moved: a subscription the old provider ran is cancelled there.
func (s *Server) releaseSource(ctx context.Context, tenant uuid.UUID, env string, done *plansv1.CompleteProviderMigrationResponse) error {
	if done.GetSourceProviderRef() == "" {
		return nil // Payrudder ran the old subscription: it ended with the cutover
	}
	prov, err := s.Resolver.Named(ctx, tenant, done.GetSourceProvider(), env)
	if err != nil {
		return err
	}
	m, ok := prov.(payments.SubscriptionManager)
	if !ok {
		return fmt.Errorf("%s cannot cancel a subscription: release %s there by hand", done.GetSourceProvider(), done.GetSourceProviderRef())
	}
	// Immediately: the customer now pays the new provider, and one more renewal here would be a second bill.
	return m.CancelSubscription(ctx, done.GetSourceProviderRef(), false)
}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	list, err := s.Subscriptions.List(r.Context(), t)
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": list})
}

// checkout asks a provider with a hosted checkout for a page where a customer subscribes.
func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	env, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	var in struct {
		Provider                string `json:"provider"`
		PriceRef                string `json:"price_ref"`
		Quantity                int    `json:"quantity"`
		PayrudderSubscriptionID string `json:"quotify_subscription_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, httpx.MaxBody)).Decode(&in); err != nil || in.Provider == "" || in.PriceRef == "" {
		httpx.Fail(w, http.StatusBadRequest, "payments.checkout.fields_required", "provider and price_ref are required")
		return
	}
	prov, err := s.Resolver.Named(r.Context(), t, in.Provider, env)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "payments.account.not_found", "no such provider account")
		return
	}
	cp, ok := prov.(payments.CheckoutProvider)
	if !ok {
		httpx.Fail(w, http.StatusConflict, "payments.checkout.unsupported", in.Provider+" has no hosted checkout")
		return
	}
	co, err := cp.CreateCheckout(r.Context(), payments.CheckoutRequest{PriceRef: in.PriceRef, Quantity: in.Quantity, PayrudderSubscriptionID: in.PayrudderSubscriptionID})
	if err != nil {
		s.Log.WarnContext(r.Context(), "checkout failed", "provider", in.Provider, "err", err)
		httpx.Fail(w, http.StatusBadGateway, "payments.provider.failed", httpx.ProviderTrouble)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]string{"url": co.URL, "ref": co.Ref})
}

// migrate moves a subscription to another payment provider (docs/14-billing-platform.md#provider-migration).
//
// To a provider that runs subscriptions itself (Paddle) the customer has to subscribe there first: a migration is started and a checkout returned. The
// old subscription is untouched until the provider confirms, so a customer who never completes the checkout costs nothing.
// To a provider Payrudder runs (iyzico) the new subscription takes over at once, or, from a provider-run subscription, at the end of the period that provider
// was paid for. The old provider is told to stop renewing first: a failure after that leaves a subscription that ends without a successor (an operator
// retries), never two that both bill.
func (s *Server) migrate(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	env, ok := httpx.Environment(w, r)
	if !ok {
		return
	}
	if s.Plans == nil {
		httpx.Fail(w, http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
		return
	}
	var in migrationInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, httpx.MaxBody)).Decode(&in); err != nil || in.SubscriptionID == "" || in.ToProvider == "" {
		httpx.Fail(w, http.StatusBadRequest, "payments.migration.fields_required", "subscription_id and to_provider are required")
		return
	}
	ctx := r.Context()
	sub, err := s.Plans.GetSubscription(ctx, t, in.SubscriptionID)
	if err != nil {
		s.planFail(w, r, err)
		return
	}
	target, err := s.Resolver.Named(ctx, t, in.ToProvider, env)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "payments.account.not_found", "no account at "+in.ToProvider+" for this tenant")
		return
	}
	if target.Capabilities().Owner() == payments.OwnerProvider {
		s.migrateViaCheckout(w, r, t, target, in)
		return
	}
	s.migrateToPayrudder(w, r, t, env, sub, in)
}

type migrationInput struct {
	SubscriptionID string `json:"subscription_id"`
	ToProvider     string `json:"to_provider"`
	PriceRef       string `json:"price_ref"`
}

// migrateViaCheckout starts a migration to a provider that runs subscriptions itself and returns the checkout the customer completes.
func (s *Server) migrateViaCheckout(w http.ResponseWriter, r *http.Request, t uuid.UUID, target payments.PaymentProvider, in migrationInput) {
	ctx := r.Context()
	cp, ok := target.(payments.CheckoutProvider)
	if !ok || in.PriceRef == "" {
		httpx.Fail(w, http.StatusBadRequest, "payments.migration.price_ref_required", in.ToProvider+" runs subscriptions itself: price_ref (its catalogue price) is required")
		return
	}
	mig, err := s.Plans.StartProviderMigration(ctx, t, &plansv1.StartProviderMigrationRequest{SubscriptionId: in.SubscriptionID, ToProvider: in.ToProvider})
	if err != nil {
		s.planFail(w, r, err)
		return
	}
	co, err := cp.CreateCheckout(ctx, payments.CheckoutRequest{PriceRef: in.PriceRef, Quantity: 1, PayrudderMigrationID: mig.GetId()})
	if err != nil {
		// No checkout, no migration: leave the subscription as it was.
		if aerr := s.Plans.AbortProviderMigration(ctx, t, mig.GetId()); aerr != nil {
			s.Log.ErrorContext(ctx, "could not abort a migration after a failed checkout", "migration", mig.GetId(), "err", aerr)
		}
		s.Log.WarnContext(ctx, "migration checkout failed", "provider", in.ToProvider, "err", err)
		httpx.Fail(w, http.StatusBadGateway, "payments.provider.failed", httpx.ProviderTrouble)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"migration": mig.GetId(), "state": "started", "checkout_url": co.URL})
}

// migrateToPayrudder moves a subscription to a provider Payrudder runs. From a provider-run subscription, it waits for the end of the period
// that provider was paid for.
func (s *Server) migrateToPayrudder(w http.ResponseWriter, r *http.Request, t uuid.UUID, env string, sub *plansv1.Subscription, in migrationInput) {
	ctx := r.Context()
	var cutover *timestamppb.Timestamp
	if sub.GetOwner() == "provider" {
		var ok bool
		if cutover, ok = s.stopRenewals(w, r, t, env, sub); !ok {
			return
		}
	}
	mig, err := s.Plans.StartProviderMigration(ctx, t, &plansv1.StartProviderMigrationRequest{SubscriptionId: in.SubscriptionID, ToProvider: in.ToProvider, CutoverTime: cutover})
	if err != nil {
		s.planFail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"migration": mig.GetId(), "state": "completed", "new_subscription": mig.GetNewSubscriptionId(),
		"cutover": mig.GetCutoverTime().AsTime()})
}

// stopRenewals tells the old provider to stop renewing a subscription at the end of its paid period, which is the cutover time.
// It reports false once it has answered.
func (s *Server) stopRenewals(w http.ResponseWriter, r *http.Request, t uuid.UUID, env string, sub *plansv1.Subscription) (*timestamppb.Timestamp, bool) {
	ctx := r.Context()
	next, found, err := s.Subscriptions.NextBilledAt(ctx, t, sub.GetProvider(), env, sub.GetProviderRef())
	if err != nil {
		httpx.Fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return nil, false
	}
	if !found {
		httpx.Fail(w, http.StatusConflict, "payments.subscription.period_end_unknown", "the end of the paid period at "+sub.GetProvider()+" is not known yet (no notification has told us)")
		return nil, false
	}
	old, err := s.Resolver.Named(ctx, t, sub.GetProvider(), env)
	if err != nil {
		httpx.Fail(w, http.StatusNotFound, "payments.account.not_found", "no account at "+sub.GetProvider()+" for this tenant")
		return nil, false
	}
	m, ok := old.(payments.SubscriptionManager)
	if !ok {
		httpx.Fail(w, http.StatusConflict, "payments.subscription.cannot_cancel", sub.GetProvider()+" cannot cancel a subscription through Payrudder")
		return nil, false
	}
	if err := m.CancelSubscription(ctx, sub.GetProviderRef(), true); err != nil {
		s.Log.WarnContext(ctx, "provider cancel failed", "provider", sub.GetProvider(), "err", err)
		httpx.Fail(w, http.StatusBadGateway, "payments.provider.failed", httpx.ProviderTrouble)
		return nil, false
	}
	return timestamppb.New(next), true
}

func (s *Server) abortMigration(w http.ResponseWriter, r *http.Request) {
	t, ok := httpx.Tenant(w, r)
	if !ok {
		return
	}
	if s.Plans == nil {
		httpx.Fail(w, http.StatusServiceUnavailable, "payments.plans.not_configured", "plans-svc is not configured")
		return
	}
	if err := s.Plans.AbortProviderMigration(r.Context(), t, r.PathValue("migration")); err != nil {
		s.planFail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"state": "aborted"})
}

// planFail relays a plans-svc refusal.
func (s *Server) planFail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.Relay(s.Log, w, r, "plans-svc", errkey.Key("payments.plans.failed"), err)
}
