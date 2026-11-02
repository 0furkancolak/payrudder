package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// Two-factor sign-in (plan 22, G9). A person who has it gets a challenge, not a session, from the first step of every sign-in method; /auth/2fa/verify is the second.
// The routes that manage it are the signed-in person's own (AccessSession); verify is public and limited per client address on top of the five tries of a challenge
// and the lock of the account (plans-svc).

// twoFactorPerMinute is the second-step and confirm attempts per minute and client address.
const twoFactorPerMinute = 10

func (s *Server) twoFactorRoutes() []Route {
	return []Route{
		{"POST", "/auth/2fa/verify", AccessPublic, "", s.twoFactorVerify},
		{"GET", "/auth/2fa", AccessSession, ratelimit.ClassRead, s.twoFactorStatus},
		{"POST", "/auth/2fa/setup", AccessSession, ratelimit.ClassWrite, s.twoFactorSetup},
		{"POST", "/auth/2fa/confirm", AccessSession, ratelimit.ClassWrite, s.twoFactorConfirm},
		{"POST", "/auth/2fa/disable", AccessSession, ratelimit.ClassWrite, s.twoFactorDisable},
		{"POST", "/auth/2fa/recovery-codes", AccessSession, ratelimit.ClassWrite, s.twoFactorRecoveryCodes},
		{"PATCH", "/app/v1/account/security", AccessSession, ratelimit.ClassWrite, s.withAccount(s.accountSecurity)},
		{"POST", "/admin/v1/users/two-factor/reset", AccessOperator, "", s.adminResetTwoFactor},
	}
}

// adminResetTwoFactor is POST /admin/v1/users/two-factor/reset {email, reason}: the operator turns off a person's two-factor sign-in after the person lost the device and
// the recovery codes. The reason is required and goes to the audit log with the operator's name; the person is mailed and signed out everywhere (plans-svc).
func (s *Server) adminResetTwoFactor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email  string `json:"email"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.ResetUserTwoFactor(r.Context(), &plansv1.ResetUserTwoFactorRequest{Email: body.Email, Reason: body.Reason})
	respond(w, r, http.StatusOK, resp, err)
}

// signedInOrChallenge is the answer of a first step: the session, or (two-factor sign-in on) {second_factor_required, challenge, challenge_expire_time}.
func (s *Server) signedInOrChallenge(w http.ResponseWriter, r *http.Request, resp *plansv1.SignInResponse, err error) {
	if err == nil && resp.GetChallenge() != "" {
		writeJSONValue(w, http.StatusOK, map[string]any{"second_factor_required": true, "challenge": resp.GetChallenge(),
			"challenge_expire_time": resp.GetChallengeExpireTime().AsTime().Format(time.RFC3339)})
		return
	}
	s.respondSignedIn(w, r, resp, err)
}

func (s *Server) twoFactorVerify(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.limits.twoFactor, twoFactorPerMinute) {
		return
	}
	var body struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Identity.VerifyTwoFactor(r.Context(), &plansv1.VerifyTwoFactorRequest{Challenge: body.Challenge, Code: body.Code})
	s.respondSignedIn(w, r, &plansv1.SignInResponse{User: resp.GetUser(), Memberships: resp.GetMemberships(), Token: resp.GetToken(), ExpireTime: resp.GetExpireTime()}, err)
}

func (s *Server) twoFactorStatus(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	resp, err := s.d.Identity.GetTwoFactor(r.Context(), &plansv1.GetTwoFactorRequest{UserId: sessionFrom(r.Context()).UserID})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) twoFactorSetup(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	resp, err := s.d.Identity.SetupTwoFactor(r.Context(), &plansv1.SetupTwoFactorRequest{UserId: sessionFrom(r.Context()).UserID})
	respond(w, r, http.StatusOK, resp, err)
}

// codeBody reads {code} and charges the per-address limit that guards the routes that take a code.
func (s *Server) codeBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.authAllowed(w, r, &s.limits.twoFactor, twoFactorPerMinute) {
		return "", false
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return "", false
	}
	noStore(w)
	return body.Code, true
}

func (s *Server) twoFactorConfirm(w http.ResponseWriter, r *http.Request) {
	code, ok := s.codeBody(w, r)
	if !ok {
		return
	}
	resp, err := s.d.Identity.ConfirmTwoFactor(r.Context(), &plansv1.ConfirmTwoFactorRequest{UserId: sessionFrom(r.Context()).UserID, Code: code})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) twoFactorDisable(w http.ResponseWriter, r *http.Request) {
	code, ok := s.codeBody(w, r)
	if !ok {
		return
	}
	if _, err := s.d.Identity.DisableTwoFactor(r.Context(), &plansv1.DisableTwoFactorRequest{UserId: sessionFrom(r.Context()).UserID, Code: code}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) twoFactorRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	code, ok := s.codeBody(w, r)
	if !ok {
		return
	}
	resp, err := s.d.Identity.RegenerateRecoveryCodes(r.Context(), &plansv1.RegenerateRecoveryCodesRequest{UserId: sessionFrom(r.Context()).UserID, Code: code})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) accountSecurity(w http.ResponseWriter, r *http.Request, sess session, id string) {
	var body struct {
		RequireTwoFactorForOwners bool `json:"require_two_factor_for_owners"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.SetAccountSecurity(r.Context(), &plansv1.SetAccountSecurityRequest{UserId: sess.UserID, AccountId: id, RequireTwoFactorForOwners: body.RequireTwoFactorForOwners})
	respond(w, r, http.StatusOK, resp, err)
}

// challengeRedirect sends the browser back from Google to the page that asks for the second factor. Like the session token of a finished login, the challenge travels
// in the fragment, which no server or proxy sees.
func (s *Server) challengeRedirect(w http.ResponseWriter, r *http.Request, challenge string, expires time.Time) {
	frag := url.Values{"challenge": {challenge}, "expires": {expires.Format(time.RFC3339)}}
	http.Redirect(w, r, s.d.AppURL+"/signin/complete#"+frag.Encode(), http.StatusFound)
}

// requireSecondFactorSetup refuses everything but the routes under /auth/ to a person whom an account they own obliges to have two-factor sign-in, until they do:
// they can reach the page that sets it up, sign out and see who they are, and nothing else.
func requireSecondFactorSetup(u *plansv1.User, path string) error {
	if u.GetTwoFactorSetupRequired() && !strings.HasPrefix(path, "/auth/") {
		return errkey.New(codes.PermissionDenied, "identity.two_factor.setup_required", "an organisation you own requires two-factor sign-in: set it up first")
	}
	return nil
}
