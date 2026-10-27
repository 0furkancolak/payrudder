// Package httpapi is the gateway's public HTTP/JSON API: routing, the middleware chain
// (recover → otel → request id → body limit → auth → tenant ctx → rate limit), and JSON ↔ gRPC mapping.
// It holds no business logic.
package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"
	"github.com/0furkancolak/payrudder/services/gateway/internal/oidc"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// MaxBodyBytes is the request body limit of every route, POST /v1/events included (1 MiB, ≤ 500 events).
const MaxBodyBytes = 1 << 20

// Authenticator verifies API keys.
type Authenticator interface {
	Authenticate(ctx context.Context, plaintext string) (auth.Principal, error)
}

// Limiter charges rate-limit cost.
type Limiter interface {
	Allow(ctx context.Context, tenantID, tier string, class ratelimit.Class, cost int) (ratelimit.Decision, error)
}

// Deps are the upstreams of the API.
type Deps struct {
	Plans           plansv1.PlansServiceClient
	Admin           plansv1.AdminServiceClient
	Identity        plansv1.IdentityServiceClient
	Webhooks        plansv1.WebhookServiceClient
	Affiliates      plansv1.AffiliateServiceClient
	AffiliatePub    plansv1.AffiliatePublicServiceClient
	AffiliatePortal plansv1.AffiliatePortalServiceClient
	Ingest          ingestv1.IngestServiceClient
	Usage           aggregatorv1.UsageQueryServiceClient
	Replay          aggregatorv1.ReplayServiceClient
	Limits          limitsv1.LimitsServiceClient
	// Saas is Payrudder as a SaaS (phase 55); nil with PAYRUDDER_SAAS=off: no plans, no limits.
	Saas          plansv1.SaasServiceClient
	Analytics     analyticsv1.AnalyticsServiceClient
	Assets        assetsv1.AssetsServiceClient
	Billing       billingv1.BillingServiceClient
	Sandbox       billingv1.SandboxServiceClient
	BillingAdmin  billingv1.BillingAdminServiceClient
	Hosted        billingv1.HostedInvoiceServiceClient
	Auth          Authenticator
	Limiter       Limiter
	OperatorToken string
	Log           *slog.Logger
	// PaymentsURL is payments-worker's HTTP address (provider webhooks, provider accounts, the ledger); nil answers 503 on those routes.
	PaymentsURL   *url.URL
	PaymentsToken string
	// PublicURL is the address customers reach the gateway at (https://api.example.com), for the links it hands out. Empty gives relative links.
	PublicURL string
	// TrustForwardedFor makes the sign-in limits count by the address a proxy in front passes in X-Forwarded-For (the console's proxy does), but only for
	// a request that really arrived from a private or loopback address, so a client cannot choose its own bucket by sending the header.
	TrustForwardedFor bool
	// TrustedProxies are networks (PAYRUDDER_TRUSTED_PROXIES) whose connections are believed about the client address, even when public.
	TrustedProxies []netip.Prefix
	// TrustedProxySecret (PAYRUDDER_TRUSTED_PROXY_SECRET) is the shared secret an edge proxy sends in X-Edge-Auth; a request carrying it is believed about the
	// client address wherever it comes from. Empty: the header is ignored.
	TrustedProxySecret string
	// Demo makes the hosted pages offer the mock payment for every tenant (PAYRUDDER_DEMO); a sandbox tenant always gets it.
	Demo bool
	// OperatorEmails lists the addresses of the people who are platform operators (PAYRUDDER_OPERATOR_EMAILS), lower case. A session of a user with one of these
	// addresses, proven by verification, may use the operator routes; OperatorToken stays as the machine credential of the API, payrudderctl and the tests.
	OperatorEmails []string
	// Google is the "Continue with Google" client; nil or unconfigured hides the button and answers 404 on its routes.
	Google *oidc.Client
	// AppURL is the console's public address (https://app.example.com), where the browser is sent after a provider login. The OAuth redirect address
	// registered at Google is AppURL + /gw/auth/google/callback unless the deployment says otherwise.
	AppURL string
}

// Server is the HTTP handler.
type Server struct {
	d       Deps
	handler http.Handler
	limits  publicLimits
	// dash remembers a dashboard for a few seconds: the same page is asked for again and again while it is open.
	dash dashCache
	// now is the clock; a request reads it once.
	now func() time.Time
	// saasEdge remembers the SaaS answers the ingest edge needs.
	saasEdge *saasEdge
}

// New builds the router.
func New(d Deps) *Server {
	s := &Server{d: d, now: time.Now, saasEdge: newSaasEdge()}
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		// otelhttp per route: the span is named after the route pattern ("POST /v1/customers"), which a global
		// handler can't know before the mux has matched.
		name := rt.Method + " " + rt.Path
		mux.Handle(name, otelhttp.NewHandler(s.wrap(rt), name,
			otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return name })))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, errkey.New(codes.NotFound, "gateway.route.not_found", "no such route"))
	})
	s.handler = requestIDMiddleware(s.recoverMiddleware(mux))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// Access is who may call a route.
type Access int

const (
	AccessTenant Access = iota
	AccessSandbox
	AccessOperator
	// AccessPublic is for callers that hold no key: a payment provider's webhook. Such a route authenticates nothing and
	// believes nothing in the request (the handler confirms with the provider).
	AccessPublic
	// AccessSession is for a signed-in person (phase 38): Authorization: Bearer qs_<session token>. The handler acts on the person's accounts only.
	AccessSession
)

// Route is one entry of the route table (also checked against api/openapi/v1.yaml by a contract test).
type Route struct {
	Method string
	Path   string
	Access Access
	Class  ratelimit.Class
	Handle func(w http.ResponseWriter, r *http.Request)
}

func (s *Server) wrap(rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("http.route", rt.Path))
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit(rt.Method, rt.Path))
		ctx, ok := s.authorize(w, r, rt, span)
		if !ok {
			return
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", requestID(r.Context()))
		if debugAllowed(rt, ctx, r) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-payrudder-debug", "1")
			span.SetAttributes(attribute.Bool("payrudder.debug", true))
		}
		rt.Handle(w, r.WithContext(ctx))
	})
}

// authorize checks the caller of a route the way its Access says and returns the context the handler runs in, carrying who they are. It writes the refusal
// itself and reports false.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, rt Route, span trace.Span) (context.Context, bool) {
	switch rt.Access {
	case AccessPublic:
		// No principal, no tenant, no rate-limit class: the worker rate-limits by never acting on unconfirmed bodies.
		return r.Context(), true
	case AccessSession:
		return s.authorizeSession(w, r, rt, span)
	case AccessOperator:
		return s.authorizeOperator(w, r)
	default:
		return s.authorizeTenant(w, r, rt, span)
	}
}

// authorizeSession admits a signed-in person, charged against the limits of their own account.
func (s *Server) authorizeSession(w http.ResponseWriter, r *http.Request, rt Route, span trace.Span) (context.Context, bool) {
	sess, err := s.session(r)
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	if !s.chargeKey(w, r, "user:"+sess.UserID, ratelimit.SessionTier, rt.Class, 1) {
		return nil, false
	}
	span.SetAttributes(attribute.String("payrudder.user_id", sess.UserID))
	return context.WithValue(r.Context(), sessionKey{}, sess), true
}

// authorizeOperator admits the machine token or the session of an operator user.
func (s *Server) authorizeOperator(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	ok, person, err := s.operatorAllowed(r)
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	if !ok {
		writeError(w, r, errkey.New(codes.Unauthenticated, "identity.operator.required", "operator access required"))
		return nil, false
	}
	ctx := tenant.WithOperator(r.Context())
	if person != "" {
		ctx = context.WithValue(ctx, operatorKey{}, person)
	}
	return ctx, true
}

// authorizeTenant admits the holder of an API key (or a person or operator acting as a tenant) to a tenant route.
func (s *Server) authorizeTenant(w http.ResponseWriter, r *http.Request, rt Route, span trace.Span) (context.Context, bool) {
	p, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	if !scopeAllows(p.Scope, rt.Method, rt.Path) {
		writeError(w, r, errkey.New(codes.PermissionDenied, "gateway.auth.scope_denied", "this API key's scope does not allow this request"))
		return nil, false
	}
	if rt.Access == AccessSandbox && p.Tenant.Environment != tenant.EnvSandbox {
		writeError(w, r, errkey.New(codes.PermissionDenied, "identity.api_key.sandbox_only", "sandbox endpoints require a qk_test_ key"))
		return nil, false
	}
	// The ingest class is charged per event, which only the handler knows after reading the body.
	if rt.Class != ratelimit.ClassIngest && !s.charge(w, r, p, rt.Class, 1) {
		return nil, false
	}
	ctx := context.WithValue(r.Context(), principalKey{}, p)
	ti := p.Tenant
	ti.Tier = p.Tier
	span.SetAttributes(attribute.String("payrudder.tenant_id", p.Tenant.ID.String()), attribute.String("payrudder.environment", p.Tenant.Environment))
	return tenant.NewContext(ctx, ti), true
}

// debugAllowed says whether X-Payrudder-Debug: 1 may force a trace through every consumer (see docs/08-observability.md). Only sandbox tenants and
// operators may.
func debugAllowed(rt Route, ctx context.Context, r *http.Request) bool {
	if r.Header.Get("X-Payrudder-Debug") != "1" {
		return false
	}
	switch rt.Access {
	case AccessOperator:
		return true
	case AccessPublic:
		return false
	}
	return principalFrom(ctx).Tenant.Environment == tenant.EnvSandbox
}

type principalKey struct{}

// charge takes cost units from the tenant's limit for class and answers 429 itself when there are none left.
func (s *Server) charge(w http.ResponseWriter, r *http.Request, p auth.Principal, class ratelimit.Class, cost int) bool {
	return s.chargeKey(w, r, p.Tenant.ID.String(), p.Tier, class, cost)
}

// chargeKey is charge for any holder of a limit: a tenant, or a signed-in person ("user:<id>").
func (s *Server) chargeKey(w http.ResponseWriter, r *http.Request, key, tier string, class ratelimit.Class, cost int) bool {
	d, err := s.d.Limiter.Allow(r.Context(), key, tier, class, cost)
	if err == nil && !d.Allowed {
		w.Header().Set("Retry-After", ratelimit.RetryAfterSeconds(d.RetryAfter))
		writeError(w, r, errkey.New(codes.ResourceExhausted, "gateway.rate_limit.exceeded", "rate limit exceeded for "+string(class)))
		return false
	}
	return true
}

func principalFrom(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey{}).(auth.Principal)
	return p
}

// ActAsHeader names the tenant an operator acts as on the tenant API.
const ActAsHeader = "X-Payrudder-Tenant"

func (s *Server) authenticate(r *http.Request) (auth.Principal, error) {
	token := bearerToken(r)
	if token == "" {
		return auth.Principal{}, errkey.New(codes.Unauthenticated, "identity.api_key.missing", "missing Authorization: Bearer <api key>")
	}
	// A person's session token, with a tenant named, is that tenant for the call, if the tenant belongs to one of the person's accounts.
	if strings.HasPrefix(token, sessionPrefix) {
		return s.sessionActAs(r)
	}
	// The operator token, with a tenant named, is that tenant for the call: the console shows a tenant's customers, plans and reports through
	// the same API the tenant uses. Without the header the operator token is no tenant key and is refused here, as before.
	if id := r.Header.Get(ActAsHeader); id != "" && s.operatorOK(r) {
		return s.actAs(r.Context(), id)
	}
	p, err := s.d.Auth.Authenticate(r.Context(), token)
	switch {
	case errors.Is(err, auth.ErrUnavailable):
		return p, errkey.New(codes.Unavailable, "identity.auth.unavailable", "authentication temporarily unavailable")
	case errors.Is(err, auth.ErrSuspended):
		return p, errkey.New(codes.PermissionDenied, "gateway.auth.tenant_not_active", "this environment is suspended or not active yet: contact support")
	case err != nil:
		return p, errkey.New(codes.Unauthenticated, "identity.api_key.invalid", "invalid api key")
	}
	return p, nil
}

// scopeAllows reports whether an API key of the scope may call the route: an ingest key only sends events, a read key only reads. Callers that are not
// API keys carry no scope and are not limited here.
func scopeAllows(scope, method, path string) bool {
	switch scope {
	case auth.ScopeIngest:
		return method == http.MethodPost && path == "/v1/events"
	case auth.ScopeRead:
		return method == http.MethodGet || method == http.MethodHead
	}
	return true
}

// actAs resolves the tenant an operator acts as.
func (s *Server) actAs(ctx context.Context, id string) (auth.Principal, error) {
	tid, err := uuid.Parse(id)
	if err != nil {
		return auth.Principal{}, errkey.New(codes.InvalidArgument, "identity.tenant.bad_act_as", ActAsHeader+" must be a tenant id")
	}
	resp, err := s.d.Admin.GetTenant(tenant.WithOperator(ctx), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return auth.Principal{}, errkey.New(codes.NotFound, "identity.tenant.not_found", "no such tenant")
		}
		return auth.Principal{}, errkey.New(codes.Unavailable, "identity.tenant.lookup_unavailable", "tenant lookup unavailable")
	}
	return principalOf(resp.GetTenant())
}

// principalOf is the principal of a tenant: what a request acting as that tenant carries. A tenant whose id is not a UUID is plans-svc's fault, not the
// caller's: the request is refused with 503 rather than sent on to the other services as the nil tenant.
func principalOf(t *plansv1.Tenant) (auth.Principal, error) {
	id, err := uuid.Parse(t.GetId())
	if err != nil {
		return auth.Principal{}, errkey.New(codes.Unavailable, "identity.tenant.lookup_unavailable", "tenant lookup unavailable")
	}
	env := tenant.EnvLive
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
		env = tenant.EnvSandbox
	}
	return auth.Principal{Tenant: tenant.Info{ID: id, Environment: env, Timezone: t.GetTimezone()}, Tier: auth.TierName(t.GetTier())}, nil
}

// operatorOK reports whether the request carries the machine operator token (PAYRUDDER_OPERATOR_TOKEN): the credential of the API, payrudderctl, the demo and the
// tests. It is never typed into a page.
func (s *Server) operatorOK(r *http.Request) bool {
	return s.d.OperatorToken != "" && subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.d.OperatorToken)) == 1
}

// operatorKey carries the address of the signed-in operator who makes an operator request. A request with the machine token carries none.
type operatorKey struct{}

// operatorFrom is the address of the signed-in operator behind ctx, or "" (the machine token, or not an operator request).
func operatorFrom(ctx context.Context) string {
	who, _ := ctx.Value(operatorKey{}).(string)
	return who
}

// operatorAllowed is who may use the operator routes: the machine token, or the session of an operator user (see isOperator). The session is resolved on
// every request, so signing out, a revoked session or a removed address takes effect at once; nothing the client sends decides it.
// An unreachable plans-svc is an error (503), not a refusal, so a signed-in operator is not told to sign in again because of an outage.
// person is the operator's verified address when a session let them in, "" for the machine token.
func (s *Server) operatorAllowed(r *http.Request) (ok bool, person string, err error) {
	if s.operatorOK(r) {
		return true, "", nil
	}
	if !strings.HasPrefix(bearerToken(r), sessionPrefix) {
		return false, "", nil
	}
	sess, err := s.session(r)
	switch {
	case status.Code(err) == codes.Unavailable:
		return false, "", err
	case err != nil:
		return false, "", nil
	case !s.isOperator(sess.User):
		// A valid session that is not an operator's: signed in, but not allowed (403), so the page does not mistake it for an expired session.
		return false, "", errkey.New(codes.PermissionDenied, "identity.operator.forbidden", "this area is for platform operators")
	}
	return true, normalizeEmail(sess.User.GetEmail()), nil
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				s.d.Log.ErrorContext(r.Context(), "http handler panic", "panic", p, "stack", string(debug.Stack()))
				writeError(w, r, status.Error(codes.Internal, "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type requestIDKey struct{}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestIDMiddleware accepts a well-formed X-Request-Id or generates one, and echoes it.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !requestIDRe.MatchString(id) {
			id = idgen.NewULID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// respond writes a proto message as JSON, or the error envelope.
func respond(w http.ResponseWriter, r *http.Request, code int, msg proto.Message, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	b, err := marshal.Marshal(msg)
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "encode response"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && code == http.StatusOK {
		// A read can be revalidated cheaply by whatever sits in front of the gateway (a CDN, an edge worker, the caller's HTTP cache): the tag is a hash of
		// the body, "private, no-cache" lets it keep a copy but not use it unasked, and the answer depends on who asks.
		sum := sha256.Sum256(b)
		tag := `W/"` + hex.EncodeToString(sum[:8]) + `"`
		h := w.Header()
		h.Set("ETag", tag)
		h.Set("Vary", "Authorization, X-Payrudder-Tenant")
		if h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "private, no-cache")
		}
		if etagMatches(r.Header.Get("If-None-Match"), tag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// etagMatches reports whether an If-None-Match header names tag (weak comparison, as the spec asks for GET).
func etagMatches(header, tag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == strings.TrimPrefix(tag, "W/") {
			return true
		}
	}
	return false
}
