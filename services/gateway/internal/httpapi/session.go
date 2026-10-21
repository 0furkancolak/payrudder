package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/services/gateway/internal/auth"
)

// People sign in at /auth/* and use the product API at /app/v1/* with a session token: Authorization: Bearer qs_<token>. The token is opaque and random;
// plans-svc stores only its hash and checks it on every request (a primary-key lookup), so a sign-out or a revocation takes effect at once. The gateway
// keeps no session state of its own. A session can also act on a tenant of /v1/* (X-Quotify-Tenant) when the tenant belongs to one of its accounts.

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

// session resolves the bearer token of r. Anything but a live session token is 401; an unreachable plans-svc is 503, never "signed out".
func (s *Server) session(r *http.Request) (session, error) {
	token := bearerToken(r)
	if !strings.HasPrefix(token, sessionPrefix) {
		return session{}, errkey.New(codes.Unauthenticated, "identity.session.invalid", "sign in to continue")
	}
	resp, err := s.d.Identity.GetSession(r.Context(), &plansv1.GetSessionRequest{Token: token})
	switch status.Code(err) {
	case codes.OK:
	case codes.Unauthenticated, codes.InvalidArgument, codes.NotFound:
		return session{}, errkey.New(codes.Unauthenticated, "identity.session.invalid", "sign in to continue")
	case codes.Unavailable, codes.DeadlineExceeded:
		return session{}, status.Error(codes.Unavailable, "sign-in is temporarily unavailable")
	default:
		return session{}, err
	}
	return session{Token: token, UserID: resp.GetUser().GetId(), User: resp.GetUser(), Memberships: resp.GetMemberships(), Expire: resp.GetExpireTime().AsTime()}, nil
}

// sessionActAs lets a session act as one tenant on the tenant API. The tenant must belong to an app of an account the person is a member of
// (plans-svc checks it in SQL through the membership); a viewer may only read.
func (s *Server) sessionActAs(r *http.Request) (auth.Principal, error) {
	id := r.Header.Get(ActAsHeader)
	if id == "" {
		return auth.Principal{}, status.Error(codes.Unauthenticated, "a session token needs "+ActAsHeader+": the tenant to act on")
	}
	sess, err := s.session(r)
	if err != nil {
		return auth.Principal{}, err
	}
	resp, err := s.d.Identity.ResolveMyTenant(r.Context(), &plansv1.ResolveMyTenantRequest{UserId: sess.UserID, TenantId: id})
	switch status.Code(err) {
	case codes.OK:
	case codes.NotFound, codes.InvalidArgument:
		return auth.Principal{}, errkey.New(codes.NotFound, "identity.tenant.not_found", "no such tenant")
	case codes.Unavailable, codes.DeadlineExceeded:
		return auth.Principal{}, status.Error(codes.Unavailable, "tenant lookup unavailable")
	default:
		return auth.Principal{}, err
	}
	if resp.GetRole() == "viewer" && r.Method != http.MethodGet && r.Method != http.MethodHead {
		return auth.Principal{}, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account is read-only")
	}
	return principalOf(resp.GetTenant()), nil
}

// clientAddr is the address a public request is counted under. Behind the console's nginx the connection comes from the proxy, which passes the visitor's
// address in X-Forwarded-For; that header is believed only when the deployment says so and the connection itself is from a private or loopback address.
func (s *Server) clientAddr(r *http.Request) string {
	remote := clientIP(r)
	if !s.d.TrustForwardedFor {
		return remote
	}
	if ip, err := netip.ParseAddr(remote); err != nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
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
	if l.allowN(s.clientAddr(r), time.Now(), perMinute) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeError(w, r, errkey.New(codes.ResourceExhausted, "identity.auth.rate_limited", "too many attempts from this address: try again in a minute"))
	return false
}

// noStore marks an answer that holds a secret (a session token, an API key) as not to be kept by any cache.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

// signUp answers 202 whether the address was free or taken: the person signs in (or opens the mailed link). It opens no session itself.
func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.signUps, signUpsPerMinute) {
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
	if !s.authAllowed(w, r, &s.signIns, signInsPerMinute) {
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
	respond(w, r, http.StatusOK, resp, err)
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
	respond(w, r, http.StatusOK, &plansv1.GetSessionResponse{User: sess.User, Memberships: sess.Memberships, ExpireTime: timestamppb.New(sess.Expire)}, nil)
}

// decodeJSON reads a JSON object body into v. Fields v does not name are ignored: the handler never takes a user, account or tenant id from a body.
func decodeJSON(r *http.Request, v any) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return errTooLarge{limit: mbe.Limit}
		}
		return status.Error(codes.InvalidArgument, "unreadable body")
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return status.Error(codes.InvalidArgument, "malformed JSON body")
	}
	return nil
}
