package httpapi

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"
)

// People sign in at /auth/* and use the product API at /app/v1/* with a session token: Authorization: Bearer qs_<token>. The token is opaque and random;
// plans-svc stores only its hash and checks it on every request (a primary-key lookup), so a sign-out or a revocation takes effect at once. The gateway
// keeps no session state of its own. A session can also act on a tenant of /v1/* (X-Payrudder-Tenant) when the tenant belongs to one of its accounts.

// sessionPrefix starts every session token (an API key starts with qk_).
const sessionPrefix = "qs_"

// session is a resolved session token.
type session struct {
	Token       string
	UserID      string
	User        *plansv1.User
	Memberships []*plansv1.Membership
	Expire      time.Time
}

type sessionKey struct{}

func sessionFrom(ctx context.Context) session {
	s, _ := ctx.Value(sessionKey{}).(session)
	return s
}

func bearerToken(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token
}

// session resolves the bearer token of r. Anything but a live session token is 401; an unreachable plans-svc is 503, never "signed out". A token plans-svc did not know is
// remembered for a few seconds (badSessions) and counts against the caller's address: random tokens would otherwise cost a plans-svc call each.
func (s *Server) session(r *http.Request) (session, error) {
	token := bearerToken(r)
	if !strings.HasPrefix(token, sessionPrefix) {
		return session{}, errSessionInvalid()
	}
	if s.badSessions.Has(token) {
		// The same token again is not a new guess: it is counted in the metric, not against the address.
		s.countAuthFailure(r, "session")
		return session{}, errSessionInvalid()
	}
	resp, err := s.d.Identity.GetSession(r.Context(), &plansv1.GetSessionRequest{Token: token})
	switch status.Code(err) {
	case codes.OK:
	case codes.Unauthenticated, codes.InvalidArgument, codes.NotFound:
		s.badSessions.Add(token)
		s.authFailed(r, "session")
		return session{}, errSessionInvalid()
	case codes.Unavailable, codes.DeadlineExceeded:
		return session{}, errkey.New(codes.Unavailable, "identity.session.unavailable", "sign-in is temporarily unavailable")
	default:
		return session{}, err
	}
	u := resp.GetUser()
	if s.d.OperatorRequireTwoFactor && !u.GetTwoFactorEnabled() && s.isOperator(u) {
		// The deployment asks two-factor sign-in of its operators: they are treated as an owner who must set it up, here and on the console's side (/auth/me says so).
		u.TwoFactorSetupRequired = true
	}
	return session{Token: token, UserID: resp.GetUser().GetId(), User: u, Memberships: resp.GetMemberships(), Expire: resp.GetExpireTime().AsTime()}, nil
}

func errSessionInvalid() error {
	return errkey.New(codes.Unauthenticated, "identity.session.invalid", "sign in to continue")
}

// sessionActAs lets a session act as one tenant on the tenant API. The tenant must belong to an app of an account the person is a member of
// (plans-svc checks it in SQL through the membership); a viewer may only read.
func (s *Server) sessionActAs(r *http.Request) (auth.Principal, error) {
	id := r.Header.Get(ActAsHeader)
	if id == "" {
		return auth.Principal{}, errkey.New(codes.Unauthenticated, "identity.session.act_as_required", "a session token needs "+ActAsHeader+": the tenant to act on")
	}
	sess, err := s.session(r)
	if err == nil {
		err = requireSecondFactorSetup(sess.User, r.URL.Path)
	}
	if err != nil {
		return auth.Principal{}, err
	}
	// An operator's session acts as any tenant, like the machine token does (the console shows a tenant's data through the tenant API).
	if s.isOperator(sess.User) {
		p, err := s.actAs(r.Context(), id)
		p.Actor = audit.Actor{Type: audit.ActorOperator, ID: sess.User.GetEmail(), Email: sess.User.GetEmail()}
		return p, err
	}
	resp, err := s.d.Identity.ResolveMyTenant(r.Context(), &plansv1.ResolveMyTenantRequest{UserId: sess.UserID, TenantId: id})
	switch status.Code(err) {
	case codes.OK:
	case codes.NotFound, codes.InvalidArgument:
		return auth.Principal{}, errkey.New(codes.NotFound, "identity.tenant.not_found", "no such tenant")
	case codes.Unavailable, codes.DeadlineExceeded:
		return auth.Principal{}, errkey.New(codes.Unavailable, "identity.tenant.lookup_unavailable", "tenant lookup unavailable")
	default:
		return auth.Principal{}, err
	}
	if resp.GetRole() == "viewer" && r.Method != http.MethodGet && r.Method != http.MethodHead {
		return auth.Principal{}, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account is read-only")
	}
	p, err := principalOf(resp.GetTenant())
	p.Actor, p.MemberRole = audit.Actor{Type: audit.ActorUser, ID: sess.UserID, Email: sess.User.GetEmail()}, resp.GetRole()
	return p, err
}

// EdgeAuthHeader carries the shared secret (PAYRUDDER_TRUSTED_PROXY_SECRET) that an edge proxy such as the console's Worker adds to prove it is the one
// that wrote the forwarded address headers.
const EdgeAuthHeader = "X-Edge-Auth"

// proxyTrusted says whether the headers naming the client address (X-Forwarded-For) were written by a proxy of ours, not by the client. Any one of three
// proofs is enough: the shared secret in X-Edge-Auth (constant-time compare; the choice for Cloudflare, whose egress addresses are not stable), a peer inside
// a configured trusted_proxies network, or, with TrustForwardedFor, a private or loopback peer.
func (s *Server) proxyTrusted(r *http.Request, remote string) bool {
	if secret := s.d.TrustedProxySecret; secret != "" {
		if got := r.Header.Get(EdgeAuthHeader); got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1 {
			return true
		}
	}
	ip, err := netip.ParseAddr(remote)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.d.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return s.d.TrustForwardedFor && (ip.IsPrivate() || ip.IsLoopback())
}

// clientAddr is the address a public request is counted under. Behind a proxy the connection comes from the proxy, which passes the visitor's address in
// X-Forwarded-For; that header is believed only from a trusted proxy (see proxyTrusted), so a client cannot choose its own bucket by sending it.
func (s *Server) clientAddr(r *http.Request) string {
	remote := clientIP(r)
	if !s.proxyTrusted(r, remote) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	last := strings.TrimSpace(xff[strings.LastIndexByte(xff, ',')+1:])
	if net.ParseIP(last) == nil {
		return remote
	}
	return last
}

// Sign-in and sign-up attempts per minute and client address. The account lock in plans-svc handles one account being guessed; these handle one address
// trying many.
const (
	signInsPerMinute = 10
	signUpsPerMinute = 5
)

func (s *Server) authAllowed(w http.ResponseWriter, r *http.Request, l *hostedLimiter, perMinute int) bool {
	if l.allowN(s.limitAddr(r), s.now(), perMinute) {
		return true
	}
	writeAuthRateLimited(w, r)
	return false
}

func writeAuthRateLimited(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", retryAfterMinute)
	writeError(w, r, errkey.New(codes.ResourceExhausted, "identity.auth.rate_limited", "too many attempts from this address: try again in a minute"))
}

// limitAddr is the key the per-address limits count a request under: the client address, an IPv6 one by its /64 (limitKey).
func (s *Server) limitAddr(r *http.Request) string { return limitKey(s.clientAddr(r)) }

// authFailuresPerMinute is how many credentials an address may present and have refused in a minute before it is refused outright.
const authFailuresPerMinute = 30

// authFailed counts a credential that was presented and refused against the caller's address, and in gateway_auth_failures under reason. An API key refused inside
// internal/auth is already in that counter: pass "" for it.
func (s *Server) authFailed(r *http.Request, reason string) {
	s.limits.authFailures.allowN(s.limitAddr(r), s.now(), authFailuresPerMinute)
	s.countAuthFailure(r, reason)
}

func (s *Server) countAuthFailure(r *http.Request, reason string) {
	if reason != "" && s.d.AuthFailures != nil {
		s.d.AuthFailures.Add(r.Context(), 1, metric.WithAttributes(attribute.String("reason", reason)))
	}
}

// authThrottled answers 429 to a request that presents a credential from an address that has had authFailuresPerMinute credentials refused in the last minute, before
// any key or session is looked up. The machine operator token is checked in memory and is never refused here; a request with no credential is not an authentication.
func (s *Server) authThrottled(w http.ResponseWriter, r *http.Request) bool {
	if bearerToken(r) == "" || s.operatorOK(r) || !s.limits.authFailures.over(s.limitAddr(r), s.now(), authFailuresPerMinute) {
		return false
	}
	s.countAuthFailure(r, "throttled")
	writeAuthRateLimited(w, r)
	return true
}

// noStore marks an answer that holds a secret (a session token, an API key) as not to be kept by any cache.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

// signUp answers 202 whether the address was free or taken: the person signs in (or opens the mailed link). It opens no session itself.
func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.limits.signUps, signUpsPerMinute) {
		return
	}
	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		AccountName string `json:"account_name"`
		Locale      string `json:"locale"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	_, err := s.d.Identity.SignUp(r.Context(), &plansv1.SignUpRequest{Email: body.Email, Password: body.Password, DisplayName: body.DisplayName,
		AccountName: body.AccountName, Locale: localeOf(r, body.Locale)})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.limits.signIns, signInsPerMinute) {
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Identity.SignIn(r.Context(), &plansv1.SignInRequest{Email: body.Email, Password: body.Password})
	s.signedInOrChallenge(w, r, resp, err)
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if _, err := s.d.Identity.SignOut(r.Context(), &plansv1.SignOutRequest{Token: sess.Token}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	noStore(w)
	s.writeWithOperator(w, r, http.StatusOK, sess.User, &plansv1.GetSessionResponse{User: sess.User, Memberships: sess.Memberships, ExpireTime: timestamppb.New(sess.Expire)})
}

// auditActor is who makes a request, for the audit log (pkg/audit): the API key, the signed-in person (or the operator behind a session that acts as a tenant), the
// operator's address or the machine token's X-Payrudder-Actor, and for every request the client address and the request id. A public route has no actor: its command
// names the person it found (a sign-in).
func (s *Server) auditActor(ctx context.Context, r *http.Request) audit.Actor {
	a := principalFrom(ctx).Actor
	switch who := operatorFrom(ctx); {
	case a.Type != "":
	case who != "":
		a = audit.Actor{Type: audit.ActorOperator, ID: who, Email: who}
	case tenant.IsOperator(ctx):
		a = audit.Actor{Type: audit.ActorOperator, ID: actorOf(r.WithContext(ctx))}
	case sessionFrom(ctx).UserID != "":
		sess := sessionFrom(ctx)
		a = audit.Actor{Type: audit.ActorUser, ID: sess.UserID, Email: sess.User.GetEmail()}
	}
	a.IP, a.RequestID = s.clientAddr(r), requestID(r.Context())
	return a
}
