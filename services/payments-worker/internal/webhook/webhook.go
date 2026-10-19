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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/handler"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
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
	Log           *slog.Logger
	// Token guards /admin. Empty disables the admin endpoints entirely.
	Token string
	// Plans, when set, is told about subscriptions a provider runs, so plans-svc can link them to ours and follow their status, and runs
	// provider migrations.
	Plans PlansAPI
}

// PlansAPI is what payments-worker asks of plans-svc, as the tenant.
type PlansAPI interface {
	SyncProviderSubscription(ctx context.Context, tenant uuid.UUID, req *plansv1.SyncProviderSubscriptionRequest) error
	GetSubscription(ctx context.Context, tenant uuid.UUID, id string) (*plansv1.Subscription, error)
	StartProviderMigration(ctx context.Context, tenant uuid.UUID, req *plansv1.StartProviderMigrationRequest) (*plansv1.ProviderMigration, error)
	CompleteProviderMigration(ctx context.Context, tenant uuid.UUID, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error)
	AbortProviderMigration(ctx context.Context, tenant uuid.UUID, id string) error
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
	mux.HandleFunc("POST /webhooks/{provider}/{tenant}/{environment}", s.tenantNotification)
	mux.HandleFunc("GET /admin/tenants/{tenant}/provider-subscriptions", s.admin(s.listSubscriptions))
	mux.HandleFunc("POST /admin/tenants/{tenant}/checkouts", s.admin(s.checkout))
	mux.HandleFunc("POST /admin/tenants/{tenant}/migrations", s.admin(s.migrate))
	mux.HandleFunc("POST /admin/tenants/{tenant}/migrations/{migration}/abort", s.admin(s.abortMigration))
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

// notification handles a webhook of a provider that names no tenant in its URL (iyzico): the charge it is about is found by the
// idempotency key in the body, and that charge's tenant and environment decide whose account confirms it.
func (s *Server) notification(w http.ResponseWriter, r *http.Request) {
	s.receive(w, r, r.PathValue("provider"), uuid.Nil, "")
}

// tenantNotification handles a webhook whose URL names the tenant and environment (Paddle: one notification destination per account).
func (s *Server) tenantNotification(w http.ResponseWriter, r *http.Request) {
	t, err := uuid.Parse(r.PathValue("tenant"))
	env := r.PathValue("environment")
	if err != nil || (env != "sandbox" && env != "live") {
		fail(w, http.StatusNotFound, "no such webhook")
		return
	}
	s.receive(w, r, r.PathValue("provider"), t, env)
}

// receive is the shared path of a notification. With tenant set the URL decided whose account it is; otherwise the body's charge does.
func (s *Server) receive(w http.ResponseWriter, r *http.Request, name string, tenantID uuid.UUID, env string) {
	ctx := r.Context()
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
	ignore := func(tenant uuid.UUID, why string) {
		finish(tenant, "ignored", why)
		s.result(ctx, name, "ignored")
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
	}

	var at store.Attempt
	if tenantID == uuid.Nil {
		var found bool
		if at, found, err = s.Attempts.ChargeByKey(ctx, ref.IdempotencyKey); err != nil {
			retry(uuid.Nil, err)
			return
		}
		if !found {
			ignore(uuid.Nil, "no charge with this reference")
			return
		}
		tenantID, env = at.TenantID, at.Environment
	}
	prov, err := s.Resolver.Named(ctx, tenantID, name, env)
	if err != nil {
		retry(tenantID, err)
		return
	}
	n, ok := prov.(payments.Notifier)
	if !ok {
		ignore(tenantID, "provider cannot confirm notifications")
		return
	}
	conf, err := n.ConfirmNotification(ctx, body, r.Header)
	if err != nil {
		if errors.Is(err, payments.ErrUnverified) {
			// Not from the provider: nothing to retry. Forget it so a genuine event with the same id is not taken for a duplicate.
			s.Log.WarnContext(ctx, "webhook failed verification", "provider", name, "tenant", tenantID, "err", err)
			_ = s.Events.Forget(ctx, name, ref.EventID)
			s.result(ctx, name, "unverified")
			fail(w, http.StatusUnauthorized, "verification failed")
			return
		}
		retry(tenantID, err)
		return
	}

	switch conf.Kind {
	case payments.KindIgnored:
		ignore(tenantID, "event not used")
		return
	case payments.KindSubscription:
		if err := s.recordSubscription(ctx, tenantID, name, env, conf); err != nil {
			retry(tenantID, err)
			return
		}
	default:
		kind := "charge"
		if conf.Kind == payments.KindRefund {
			kind = "refund"
		}
		entry := store.LedgerEntry{TenantID: tenantID, Provider: name, Environment: env, Kind: kind, AmountMinor: conf.AmountMinor, Currency: conf.Currency,
			ProviderRef: conf.ProviderRef, IdempotencyKey: conf.Ref.IdempotencyKey, Source: "webhook"}
		if at.InvoiceID != uuid.Nil {
			entry.InvoiceID = at.InvoiceID
			if entry.AmountMinor == 0 {
				entry.AmountMinor, entry.Currency = at.AmountMinor, at.Currency
			}
		} else if conf.Ref.IdempotencyKey != "" {
			// A charge of ours the provider settled later (a notification found by its key rather than by the URL).
			if a, found, err := s.Attempts.ChargeByKey(ctx, conf.Ref.IdempotencyKey); err == nil && found && a.TenantID == tenantID {
				entry.InvoiceID = a.InvoiceID
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
			retry(tenantID, errors.New("provider did not report a final state"))
			return
		}
		if entry.Currency == "" {
			entry.Currency = "XXX" // the provider did not say; the ledger requires a code
		}
		if _, err := s.Ledger.Append(ctx, entry); err != nil {
			retry(tenantID, err)
			return
		}
		if conf.ProviderRef != "" {
			if n, err := s.Ledger.Outcomes(ctx, name, kind, conf.ProviderRef); err == nil && n > 1 {
				discrepancy.Add(ctx, 1, metric.WithAttributes(attribute.String("provider", name)))
				s.Log.ErrorContext(ctx, "payment outcomes disagree", "provider", name, "ref", conf.ProviderRef, "tenant", tenantID, "invoice", at.InvoiceID)
			}
		}
	}
	finish(tenantID, "processed", "")
	s.result(ctx, name, "processed")
	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

func (s *Server) recordSubscription(ctx context.Context, tenant uuid.UUID, provider, env string, n payments.Notification) error {
	ev := n.Subscription
	if ev == nil {
		return errors.New("subscription notification without a subscription")
	}
	var q *uuid.UUID
	if id, err := uuid.Parse(ev.QuotifySubscriptionID); err == nil {
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
	if mig := ev.QuotifyMigrationID; mig != "" && s.Plans != nil {
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
		known, found, err := s.Subscriptions.QuotifyID(ctx, tenant, provider, env, ev.ProviderRef)
		if err != nil {
			return err
		}
		if !found {
			return nil // a subscription we did not start: kept for the operator, nothing to link
		}
		q = &known
	}
	return s.Plans.SyncProviderSubscription(ctx, tenant, &plansv1.SyncProviderSubscriptionRequest{
		SubscriptionId: q.String(), Provider: provider, ProviderRef: ev.ProviderRef, Owner: owner, ProviderStatus: ev.Status, OccurredAt: timestamppb.New(occurred),
	})
}

// releaseSource stops the old provider from billing a subscription that has moved: a subscription the old provider ran is cancelled there.
func (s *Server) releaseSource(ctx context.Context, tenant uuid.UUID, env string, done *plansv1.CompleteProviderMigrationResponse) error {
	if done.GetSourceProviderRef() == "" {
		return nil // Quotify ran the old subscription: it ended with the cutover
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

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	list, err := s.Subscriptions.List(r.Context(), t)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": list})
}

// checkout asks a provider with a hosted checkout for a page where a customer subscribes.
func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	env, ok := environment(w, r)
	if !ok {
		return
	}
	var in struct {
		Provider              string `json:"provider"`
		PriceRef              string `json:"price_ref"`
		Quantity              int    `json:"quantity"`
		QuotifySubscriptionID string `json:"quotify_subscription_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil || in.Provider == "" || in.PriceRef == "" {
		fail(w, http.StatusBadRequest, "provider and price_ref are required")
		return
	}
	prov, err := s.Resolver.Named(r.Context(), t, in.Provider, env)
	if err != nil {
		fail(w, http.StatusNotFound, "no such provider account")
		return
	}
	cp, ok := prov.(payments.CheckoutProvider)
	if !ok {
		fail(w, http.StatusConflict, in.Provider+" has no hosted checkout")
		return
	}
	co, err := cp.CreateCheckout(r.Context(), payments.CheckoutRequest{PriceRef: in.PriceRef, Quantity: in.Quantity, QuotifySubscriptionID: in.QuotifySubscriptionID})
	if err != nil {
		s.Log.WarnContext(r.Context(), "checkout failed", "provider", in.Provider, "err", err)
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": co.URL, "ref": co.Ref})
}

// migrate moves a subscription to another payment provider (docs/14-billing-platform.md#provider-migration).
//
// To a provider that runs subscriptions itself (Paddle) the customer has to subscribe there first: a migration is started and a checkout returned. The
// old subscription is untouched until the provider confirms, so a customer who never completes the checkout costs nothing.
// To a provider Quotify runs (iyzico) the new subscription takes over at once, or, from a provider-run subscription, at the end of the period that provider
// was paid for. The old provider is told to stop renewing first: a failure after that leaves a subscription that ends without a successor (an operator
// retries), never two that both bill.
func (s *Server) migrate(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	env, ok := environment(w, r)
	if !ok {
		return
	}
	if s.Plans == nil {
		fail(w, http.StatusServiceUnavailable, "plans-svc is not configured")
		return
	}
	var in struct {
		SubscriptionID string `json:"subscription_id"`
		ToProvider     string `json:"to_provider"`
		PriceRef       string `json:"price_ref"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&in); err != nil || in.SubscriptionID == "" || in.ToProvider == "" {
		fail(w, http.StatusBadRequest, "subscription_id and to_provider are required")
		return
	}
	ctx := r.Context()
	sub, err := s.Plans.GetSubscription(ctx, t, in.SubscriptionID)
	if err != nil {
		planFail(w, err)
		return
	}
	target, err := s.Resolver.Named(ctx, t, in.ToProvider, env)
	if err != nil {
		fail(w, http.StatusNotFound, "no account at "+in.ToProvider+" for this tenant")
		return
	}
	if target.Capabilities().Owner() == payments.OwnerProvider {
		cp, ok := target.(payments.CheckoutProvider)
		if !ok || in.PriceRef == "" {
			fail(w, http.StatusBadRequest, in.ToProvider+" runs subscriptions itself: price_ref (its catalogue price) is required")
			return
		}
		mig, err := s.Plans.StartProviderMigration(ctx, t, &plansv1.StartProviderMigrationRequest{SubscriptionId: in.SubscriptionID, ToProvider: in.ToProvider})
		if err != nil {
			planFail(w, err)
			return
		}
		co, err := cp.CreateCheckout(ctx, payments.CheckoutRequest{PriceRef: in.PriceRef, Quantity: 1, QuotifyMigrationID: mig.GetId()})
		if err != nil {
			// No checkout, no migration: leave the subscription as it was.
			if aerr := s.Plans.AbortProviderMigration(ctx, t, mig.GetId()); aerr != nil {
				s.Log.ErrorContext(ctx, "could not abort a migration after a failed checkout", "migration", mig.GetId(), "err", aerr)
			}
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"migration": mig.GetId(), "state": "started", "checkout_url": co.URL})
		return
	}

	// The target is run by Quotify. From a provider-run subscription, wait for the end of the period that provider was paid for.
	var cutover *timestamppb.Timestamp
	if sub.GetOwner() == "provider" {
		next, found, err := s.Subscriptions.NextBilledAt(ctx, t, sub.GetProvider(), env, sub.GetProviderRef())
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !found {
			fail(w, http.StatusConflict, "the end of the paid period at "+sub.GetProvider()+" is not known yet (no notification has told us)")
			return
		}
		cutover = timestamppb.New(next)
		old, err := s.Resolver.Named(ctx, t, sub.GetProvider(), env)
		if err != nil {
			fail(w, http.StatusNotFound, "no account at "+sub.GetProvider()+" for this tenant")
			return
		}
		m, ok := old.(payments.SubscriptionManager)
		if !ok {
			fail(w, http.StatusConflict, sub.GetProvider()+" cannot cancel a subscription through Quotify")
			return
		}
		if err := m.CancelSubscription(ctx, sub.GetProviderRef(), true); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	mig, err := s.Plans.StartProviderMigration(ctx, t, &plansv1.StartProviderMigrationRequest{SubscriptionId: in.SubscriptionID, ToProvider: in.ToProvider, CutoverTime: cutover})
	if err != nil {
		planFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"migration": mig.GetId(), "state": "completed", "new_subscription": mig.GetNewSubscriptionId(),
		"cutover": mig.GetCutoverTime().AsTime()})
}

func (s *Server) abortMigration(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	if s.Plans == nil {
		fail(w, http.StatusServiceUnavailable, "plans-svc is not configured")
		return
	}
	if err := s.Plans.AbortProviderMigration(r.Context(), t, r.PathValue("migration")); err != nil {
		planFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": "aborted"})
}

// planFail relays a plans-svc refusal: its status code becomes the HTTP one.
func planFail(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.NotFound:
		fail(w, http.StatusNotFound, status.Convert(err).Message())
	case codes.InvalidArgument:
		fail(w, http.StatusBadRequest, status.Convert(err).Message())
	case codes.FailedPrecondition, codes.AlreadyExists, codes.Aborted:
		fail(w, http.StatusConflict, status.Convert(err).Message())
	default:
		fail(w, http.StatusBadGateway, "plans-svc: "+status.Convert(err).Message())
	}
}
