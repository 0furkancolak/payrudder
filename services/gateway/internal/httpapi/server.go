// Package httpapi is the gateway's public HTTP/JSON API: routing, the middleware chain
// (recover → otel → request id → body limit → auth → tenant ctx → rate limit), and JSON ↔ gRPC mapping.
// It holds no business logic.
package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"

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
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/gateway/internal/auth"
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
	Ingest        ingestv1.IngestServiceClient
	Usage         aggregatorv1.UsageQueryServiceClient
	Limits        limitsv1.LimitsServiceClient
	Analytics     analyticsv1.AnalyticsServiceClient
	Assets        assetsv1.AssetsServiceClient
	Billing       billingv1.BillingServiceClient
	Sandbox       billingv1.SandboxServiceClient
	BillingAdmin  billingv1.BillingAdminServiceClient
	Auth          Authenticator
	Limiter       Limiter
	OperatorToken string
	Log           *slog.Logger
}

// Server is the HTTP handler.
type Server struct {
	d       Deps
	handler http.Handler
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
		case AccessOperator:
			if !s.operatorOK(r) {
				writeError(w, r, status.Error(codes.Unauthenticated, "operator token required"))
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
		if r.Header.Get("X-Quotify-Debug") == "1" && (rt.Access == AccessOperator || principalFrom(ctx).Tenant.Environment == tenant.EnvSandbox) {
			ctx = metadata.AppendToOutgoingContext(ctx, "x-quotify-debug", "1")
			span.SetAttributes(attribute.Bool("quotify.debug", true))
		}
		rt.Handle(w, r.WithContext(ctx))
	})
}

type principalKey struct{}

// charge takes cost units from the tenant's limit for class and answers 429 itself when there are none left.
func (s *Server) charge(w http.ResponseWriter, r *http.Request, p auth.Principal, class ratelimit.Class, cost int) bool {
	d, err := s.d.Limiter.Allow(r.Context(), p.Tenant.ID.String(), p.Tier, class, cost)
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

func (s *Server) authenticate(r *http.Request) (auth.Principal, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "missing Authorization: Bearer <api key>")
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

func (s *Server) operatorOK(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.d.OperatorToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.d.OperatorToken)) == 1
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
	w.WriteHeader(code)
	_, _ = w.Write(b)
}
