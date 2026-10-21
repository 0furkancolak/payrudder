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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
	analyticsv1 "github.com/0furkancolak/quotify/gen/go/quotify/analytics/v1"
	assetsv1 "github.com/0furkancolak/quotify/gen/go/quotify/assets/v1"
	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	limitsv1 "github.com/0furkancolak/quotify/gen/go/quotify/limits/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/gateway/internal/auth"
	"github.com/0furkancolak/quotify/services/gateway/internal/oidc"
	"github.com/0furkancolak/quotify/services/gateway/internal/ratelimit"
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
	Plans         plansv1.PlansServiceClient
	Admin         plansv1.AdminServiceClient
	Identity      plansv1.IdentityServiceClient
	Webhooks      plansv1.WebhookServiceClient
	Ingest        ingestv1.IngestServiceClient
	Usage         aggregatorv1.UsageQueryServiceClient
	Replay        aggregatorv1.ReplayServiceClient
	Limits        limitsv1.LimitsServiceClient
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
	// TrustForwardedFor makes the sign-in limits count by the address a proxy in front passes in X-Forwarded-For (the console's nginx does), but only for
	// a request that really arrived from a private or loopback address, so a client cannot choose its own bucket by sending the header.
	TrustForwardedFor bool
	// Demo makes the hosted pages offer the mock payment for every tenant (QUOTIFY_DEMO); a sandbox tenant always gets it.
	Demo bool
	// OperatorEmails lists the addresses of the people who are platform operators (QUOTIFY_OPERATOR_EMAILS), lower case. A session of a user with one of these
	// addresses, proven by verification, may use the operator routes; OperatorToken stays as the machine credential of the API, quotifyctl and the tests.
	OperatorEmails []string
	// Google is the "Continue with Google" client; nil or unconfigured hides the button and answers 404 on its routes.
	Google *oidc.Client
	// AppURL is the console's public address (https://app.example.com), where the browser is sent after a provider login. The OAuth redirect address
	// registered at Google is AppURL + /auth/google/callback unless the deployment says otherwise.
	AppURL string
}

// Server is the HTTP handler.
type Server struct {
	d       Deps
	handler http.Handler
	hosted  hostedLimiter
	// hostedPays limits the POSTs that start a payment on a public page.
	hostedPays hostedLimiter
	// dash remembers a dashboard for a few seconds: the same page is asked for again and again while it is open.
	dash dashCache
	// signIns and signUps limit the public sign-in and sign-up routes, per client address.
	signIns, signUps hostedLimiter
	// resets limits "forgot password" requests and tokenUses the routes that spend a mailed token (verify, reset, accept an invitation), per client address.
	resets, tokenUses hostedLimiter
	// otpRequests, otpVerifies and oauths limit the one-time code and provider sign-in routes, per client address.
	otpRequests, otpVerifies, oauths hostedLimiter
}

// New builds the router.
func New(d Deps) *Server {
	s := &Server{d: d}
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		// otelhttp per route: the span is named after the route pattern ("POST /v1/customers"), which a global
		// handler can't know before the mux has matched.
		name := rt.Method + " " + rt.Path
		mux.Handle(name, otelhttp.NewHandler(s.wrap(rt), name,
			otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return name })))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, status.Error(codes.NotFound, "no such route"))
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

// Routes exposes the route table for the contract test.
func (s *Server) Routes() []Route { return s.routes() }

func (s *Server) wrap(rt Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("http.route", rt.Path))
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit(rt.Method, rt.Path))
		ctx := r.Context()
		switch rt.Access {
		case AccessPublic:
			// No principal, no tenant, no rate-limit class: the worker rate-limits by never acting on unconfirmed bodies.
		case AccessSession:
			sess, err := s.session(r)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if !s.chargeKey(w, r, "user:"+sess.UserID, "standard", rt.Class, 1) {
				return
			}
			ctx = context.WithValue(ctx, sessionKey{}, sess)
			span.SetAttributes(attribute.String("quotify.user_id", sess.UserID))
		case AccessOperator:
			ok, err := s.operatorAllowed(r)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if !ok {
				writeError(w, r, status.Error(codes.Unauthenticated, "operator access required"))
				return
			}
			ctx = tenant.WithOperator(ctx)
		default:
			p, err := s.authenticate(r)
			if err != nil {
				writeError(w, r, err)
				return
			}
			if rt.Access == AccessSandbox && p.Tenant.Environment != tenant.EnvSandbox {
				writeError(w, r, status.Error(codes.PermissionDenied, "sandbox endpoints require a qk_test_ key"))
				return
			}
			// The ingest class is charged per event, which only the handler knows after reading the body.
			if rt.Class != ratelimit.ClassIngest && !s.charge(w, r, p, rt.Class, 1) {
				return
			}
			ctx = context.WithValue(ctx, principalKey{}, p)
			ti := p.Tenant
			ti.Tier = p.Tier
			ctx = tenant.NewContext(ctx, ti)
			span.SetAttributes(attribute.String("quotify.tenant_id", p.Tenant.ID.String()),
				attribute.String("quotify.environment", p.Tenant.Environment))
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", requestID(r.Context()))
		// Only sandbox tenants and operators may force a trace through every consumer (see docs/08-observability.md).
		if r.Header.Get("X-Quotify-Debug") == "1" && (rt.Access == AccessOperator || (rt.Access != AccessPublic && principalFrom(ctx).Tenant.Environment == tenant.EnvSandbox)) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-quotify-debug", "1")
			span.SetAttributes(attribute.Bool("quotify.debug", true))
		}
		rt.Handle(w, r.WithContext(ctx))
	})
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
		writeError(w, r, status.Error(codes.ResourceExhausted, "rate limit exceeded for "+string(class)))
		return false
	}
	return true
}

func principalFrom(ctx context.Context) auth.Principal {
	p, _ := ctx.Value(principalKey{}).(auth.Principal)
	return p
}

// ActAsHeader names the tenant an operator acts as on the tenant API.
const ActAsHeader = "X-Quotify-Tenant"

func (s *Server) authenticate(r *http.Request) (auth.Principal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "missing Authorization: Bearer <api key>")
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
		return p, status.Error(codes.Unavailable, "authentication temporarily unavailable")
	case err != nil:
		return p, status.Error(codes.Unauthenticated, "invalid api key")
	}
	return p, nil
}

// actAs resolves the tenant an operator acts as.
func (s *Server) actAs(ctx context.Context, id string) (auth.Principal, error) {
	tid, err := uuid.Parse(id)
	if err != nil {
		return auth.Principal{}, status.Error(codes.InvalidArgument, ActAsHeader+" must be a tenant id")
	}
	resp, err := s.d.Admin.GetTenant(tenant.WithOperator(ctx), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return auth.Principal{}, status.Error(codes.NotFound, "no such tenant")
		}
		return auth.Principal{}, status.Error(codes.Unavailable, "tenant lookup unavailable")
	}
	return principalOf(resp.GetTenant()), nil
}

// principalOf is the principal of a tenant: what a request acting as that tenant carries.
func principalOf(t *plansv1.Tenant) auth.Principal {
	env := tenant.EnvLive
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX {
		env = tenant.EnvSandbox
	}
	tier := "free"
	switch t.GetTier() {
	case plansv1.TenantTier_TENANT_TIER_STANDARD:
		tier = "standard"
	case plansv1.TenantTier_TENANT_TIER_ENTERPRISE:
		tier = "enterprise"
	}
	id, _ := uuid.Parse(t.GetId())
	return auth.Principal{Tenant: tenant.Info{ID: id, Environment: env, Timezone: t.GetTimezone()}, Tier: tier}
}

// operatorOK reports whether the request carries the machine operator token (QUOTIFY_OPERATOR_TOKEN): the credential of the API, quotifyctl, the demo and the
// tests. It is never typed into a page.
func (s *Server) operatorOK(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.d.OperatorToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.d.OperatorToken)) == 1
}

// operatorAllowed is who may use the operator routes: the machine token, or the session of an operator user (see isOperator). The session is resolved on
// every request, so signing out, a revoked session or a removed address takes effect at once; nothing the client sends decides it.
// An unreachable plans-svc is an error (503), not a refusal, so a signed-in operator is not told to sign in again because of an outage.
func (s *Server) operatorAllowed(r *http.Request) (bool, error) {
	if s.operatorOK(r) {
		return true, nil
	}
	if !strings.HasPrefix(bearerToken(r), sessionPrefix) {
		return false, nil
	}
	sess, err := s.session(r)
	switch {
	case status.Code(err) == codes.Unavailable:
		return false, err
	case err != nil:
		return false, nil
	case !s.isOperator(sess.User):
		// A valid session that is not an operator's: signed in, but not allowed (403), so the page does not mistake it for an expired session.
		return false, errkey.New(codes.PermissionDenied, "identity.operator.forbidden", "this area is for platform operators")
	}
	return true, nil
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

var (
	unmarshal = protojson.UnmarshalOptions{}
	marshal   = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
)

// errTooLarge renders as 413.
type errTooLarge struct {
	limit int64
	// files means limit is a number of files, not bytes.
	files bool
}

func (e errTooLarge) Error() string {
	if e.files {
		return fmt.Sprintf("more than %d files in one request", e.limit)
	}
	return fmt.Sprintf("request body larger than %d bytes", e.limit)
}

// decode reads a JSON body into msg. An empty body leaves msg unchanged.
func decode(r *http.Request, msg proto.Message) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errTooLarge{limit: mbe.Limit}
		}
		return status.Error(codes.InvalidArgument, "unreadable body")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := unmarshal.Unmarshal(body, msg); err != nil {
		return status.Errorf(codes.InvalidArgument, "malformed JSON: %v", err)
	}
	return nil
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
		h.Set("Vary", "Authorization, X-Quotify-Tenant")
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
