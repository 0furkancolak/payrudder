package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// The three ways to sign in (phase 42): e-mail and password (signin.go's signIn), a one-time code mailed to the address, and Google. The operator console has
// no sign-in of its own any more: an operator is a person who signs in one of these ways with a verified address that PAYRUDDER_OPERATOR_EMAILS lists.

// Per-minute limits per client address on the new public routes. The per-address limits of the code (one a minute, five an hour, five tries) are plans-svc's.
const (
	otpRequestsPerMinute = 5
	otpVerifiesPerMinute = 10
	oauthPerMinute       = 20
)

// methods is GET /auth/methods: which sign-in methods this deployment offers, so the page shows only buttons that work.
func (s *Server) methods(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSONValue(w, http.StatusOK, map[string]bool{"password": true, "otp": true, "google": s.d.Google.Enabled()})
}

// isOperator reports whether a user is a platform operator. The decision is made here, on the server, from two things the client cannot influence: the address
// is on the configured list (PAYRUDDER_OPERATOR_EMAILS) and the person has proven they own it. Nothing about it is stored on the user or sent by the client.
func (s *Server) isOperator(u *plansv1.User) bool {
	if u == nil || !u.GetEmailVerified() || u.GetEmail() == "" {
		return false
	}
	email := strings.ToLower(strings.TrimSpace(u.GetEmail()))
	for _, o := range s.d.OperatorEmails {
		if subtleEqual(o, email) {
			return true
		}
	}
	return false
}

func subtleEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// NormalizeOperatorEmails turns the comma list of the setting into the list the server compares against (trimmed, lower case, empty entries dropped).
func NormalizeOperatorEmails(list []string) []string {
	out := make([]string, 0, len(list))
	for _, e := range list {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// withOperator renders a proto response as JSON with the server's verdict "operator": true|false added. The flag only tells the page whether to show the
// operator console; every operator route decides for itself again, from the session, on each request.
func (s *Server) withOperator(u *plansv1.User, body []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["operator"] = json.RawMessage(strconv.FormatBool(s.isOperator(u)))
	return json.Marshal(m)
}

// respondSignedIn writes a session response (sign-in, code, provider) or the error.
func (s *Server) respondSignedIn(w http.ResponseWriter, r *http.Request, resp *plansv1.SignInResponse, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	b, err := marshal.Marshal(resp)
	if err == nil {
		b, err = s.withOperator(resp.GetUser(), b)
	}
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "encode response"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// otpRequest is POST /auth/otp/request {email, locale}: mails a code. 202 for any well-formed address, whether or not it has an account and whether or not
// a code was actually sent (plans-svc sends at most one a minute and five an hour per address).
func (s *Server) otpRequest(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.otpRequests, otpRequestsPerMinute) {
		return
	}
	var body struct {
		Email  string `json:"email"`
		Locale string `json:"locale"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	if _, err := s.d.Identity.RequestLoginCode(r.Context(), &plansv1.RequestLoginCodeRequest{Email: body.Email, Locale: localeOf(r, body.Locale)}); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

// otpVerify is POST /auth/otp/verify {email, code, display_name, account_name, locale}: checks the code and opens a session; an address without an account
// gets one.
func (s *Server) otpVerify(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.otpVerifies, otpVerifiesPerMinute) {
		return
	}
	var body struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		DisplayName string `json:"display_name"`
		AccountName string `json:"account_name"`
		Locale      string `json:"locale"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Identity.VerifyLoginCode(r.Context(), &plansv1.VerifyLoginCodeRequest{Email: body.Email, Code: body.Code, DisplayName: body.DisplayName,
		AccountName: body.AccountName, Locale: localeOf(r, body.Locale)})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.respondSignedIn(w, r, &plansv1.SignInResponse{User: resp.GetUser(), Memberships: resp.GetMemberships(), Token: resp.GetToken(), ExpireTime: resp.GetExpireTime()}, nil)
}

// ---- Google ----

// googleCookie carries the secrets of a login in progress from /auth/google/start to /auth/google/callback: the state (compared with the one Google sends
// back), the nonce (compared with the one in the ID token) and the PKCE verifier. It is HttpOnly and signed, so the browser cannot read or change it, and it
// exists for ten minutes. The gateway keeps no state of its own, so any replica can finish a login another started.
const (
	googleCookie    = "qg_login"
	googleCookieTTL = 10 * time.Minute
)

type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Locale   string `json:"l"`
	Expires  int64  `json:"e"`
}

// stateKey is derived from the client secret, which every replica shares and an attacker does not have.
func (s *Server) stateKey() []byte {
	return s.d.Google.StateKey()
}

func (s *Server) sealState(st loginState) string {
	raw, _ := json.Marshal(st)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	m := hmac.New(sha256.New, s.stateKey())
	m.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) openState(value string) (loginState, bool) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok {
		return loginState{}, false
	}
	want := hmac.New(sha256.New, s.stateKey())
	want.Write([]byte(payload))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, want.Sum(nil)) {
		return loginState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	var st loginState
	if err != nil || json.Unmarshal(raw, &st) != nil || time.Now().Unix() > st.Expires || st.State == "" || st.Nonce == "" || st.Verifier == "" {
		return loginState{}, false
	}
	return st, true
}

func (s *Server) secureCookies() bool { return strings.HasPrefix(s.d.AppURL, "https://") }

func (s *Server) setLoginCookie(w http.ResponseWriter, value string, maxAge int) {
	//nolint:gosec // Secure follows the console's scheme: development runs on plain http, every deployment on https
	http.SetCookie(w, &http.Cookie{Name: googleCookie, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: s.secureCookies(),
		// Lax: Google returns the browser with a top-level GET, which Lax sends; a request from another site's page or form does not carry it.
		SameSite: http.SameSiteLaxMode})
}

// googleStart is GET /auth/google/start: sends the browser to Google's consent page.
func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) {
	if !s.d.Google.Enabled() {
		writeError(w, r, errkey.New(codes.NotFound, "identity.oauth.not_configured", "sign-in with Google is not set up on this deployment"))
		return
	}
	if !s.authAllowed(w, r, &s.oauths, oauthPerMinute) {
		return
	}
	req, err := s.d.Google.Begin(r.Context())
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "google sign-in could not start", "error", err)
		s.oauthFail(w, r, "unavailable")
		return
	}
	noStore(w)
	s.setLoginCookie(w, s.sealState(loginState{State: req.State, Nonce: req.Nonce, Verifier: req.Verifier, Locale: localeOf(r, r.URL.Query().Get("locale")),
		Expires: time.Now().Add(googleCookieTTL).Unix()}), int(googleCookieTTL.Seconds()))
	http.Redirect(w, r, req.URL, http.StatusFound)
}

// oauthFail sends the browser back to the sign-in page with a short reason the page turns into a message. The reason is one of a fixed few, never provider text.
func (s *Server) oauthFail(w http.ResponseWriter, r *http.Request, reason string) {
	s.setLoginCookie(w, "", -1)
	http.Redirect(w, r, s.d.AppURL+"/signin?oauth_error="+url.QueryEscape(reason), http.StatusFound)
}

// googleCallback is GET /auth/google/callback: the browser comes back from Google with ?code&state. Everything is checked before a user is touched: the cookie
// (signed, fresh), the state, the code exchange with the PKCE verifier, and the ID token (signature, issuer, audience, expiry, nonce, verified address).
// Only then does plans-svc find, link or create the user. A session token is handed to the page in the URL fragment, which no server or proxy ever sees.
func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.d.Google.Enabled() {
		writeError(w, r, errkey.New(codes.NotFound, "identity.oauth.not_configured", "sign-in with Google is not set up on this deployment"))
		return
	}
	if !s.authAllowed(w, r, &s.oauths, oauthPerMinute) {
		return
	}
	noStore(w)
	q := r.URL.Query()
	c, err := r.Cookie(googleCookie)
	if err != nil {
		s.oauthFail(w, r, "state")
		return
	}
	st, ok := s.openState(c.Value)
	// The cookie is spent whatever happens next: a login cannot be retried with the same state.
	if !ok || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1 {
		s.oauthFail(w, r, "state")
		return
	}
	if q.Get("error") != "" {
		s.oauthFail(w, r, "denied")
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 2048 {
		s.oauthFail(w, r, "state")
		return
	}
	claims, err := s.d.Google.Exchange(r.Context(), code, st.Verifier, st.Nonce)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "google sign-in refused", "reason", err.Error())
		s.oauthFail(w, r, "token")
		return
	}
	if !claims.EmailVerified {
		s.oauthFail(w, r, "email_unverified")
		return
	}
	resp, err := s.d.Identity.SignInWithIdentity(r.Context(), &plansv1.SignInWithIdentityRequest{Provider: "google", Subject: claims.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, DisplayName: claims.Name, Locale: st.Locale})
	if err != nil {
		if status.Code(err) == codes.Unavailable || status.Code(err) == codes.DeadlineExceeded {
			s.oauthFail(w, r, "unavailable")
			return
		}
		s.d.Log.WarnContext(r.Context(), "google sign-in not completed", "code", status.Code(err).String())
		s.oauthFail(w, r, "failed")
		return
	}
	s.setLoginCookie(w, "", -1)
	frag := url.Values{"token": {resp.GetToken()}, "expires": {resp.GetExpireTime().AsTime().Format(time.RFC3339)}}
	http.Redirect(w, r, s.d.AppURL+"/signin/complete#"+frag.Encode(), http.StatusFound)
}
