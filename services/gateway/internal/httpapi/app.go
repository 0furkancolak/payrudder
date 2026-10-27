package httpapi

import (
	"net/http"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/apikey"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The product API (/app/v1): what a signed-in person sees and does in their accounts. Every handler takes the user from the session and names no
// user, account or tenant from the request body; the account comes from ?account_id= (default: the person's first account) and is checked against
// the session's memberships here and again in SQL by plans-svc.

func (s *Server) appRoutes() []Route {
	return []Route{
		{"POST", "/auth/signup", AccessPublic, "", s.signUp},
		{"POST", "/auth/signin", AccessPublic, "", s.signIn},
		{"GET", "/auth/methods", AccessPublic, "", s.methods},
		{"POST", "/auth/otp/request", AccessPublic, "", s.otpRequest},
		{"POST", "/auth/otp/verify", AccessPublic, "", s.otpVerify},
		{"GET", "/auth/google/start", AccessPublic, "", s.googleStart},
		{"GET", "/auth/google/callback", AccessPublic, "", s.googleCallback},
		{"POST", "/auth/verify", AccessPublic, "", s.verifyEmail},
		{"POST", "/auth/resend-verification", AccessSession, ratelimit.ClassWrite, s.resendVerification},
		{"POST", "/auth/forgot", AccessPublic, "", s.forgotPassword},
		{"POST", "/auth/reset", AccessPublic, "", s.resetPassword},
		{"POST", "/auth/signout", AccessSession, ratelimit.ClassWrite, s.signOut},
		{"GET", "/auth/me", AccessSession, ratelimit.ClassRead, s.me},
		{"GET", "/app/v1/account", AccessSession, ratelimit.ClassRead, s.withAccount(s.appAccount)},
		{"GET", "/app/v1/apps", AccessSession, ratelimit.ClassRead, s.withAccount(s.appList)},
		{"POST", "/app/v1/apps", AccessSession, ratelimit.ClassWrite, s.withAccount(s.appCreate)},
		{"GET", "/app/v1/apps/{id}", AccessSession, ratelimit.ClassRead, s.appGet},
		{"PATCH", "/app/v1/apps/{id}", AccessSession, ratelimit.ClassWrite, s.appRename},
		{"DELETE", "/app/v1/apps/{id}", AccessSession, ratelimit.ClassWrite, s.appDelete},
		{"GET", "/app/v1/apps/{id}/overview", AccessSession, ratelimit.ClassRead, s.appOverview},
		{"GET", "/app/v1/apps/{id}/dashboard", AccessSession, ratelimit.ClassRead, s.appDashboard},
		{"GET", "/app/v1/apps/{id}/api-keys", AccessSession, ratelimit.ClassRead, s.appKeys},
		{"POST", "/app/v1/apps/{id}/api-keys", AccessSession, ratelimit.ClassWrite, s.appKeyCreate},
		{"DELETE", "/app/v1/apps/{id}/api-keys/{key_id}", AccessSession, ratelimit.ClassWrite, s.appKeyRevoke},
	}
}

// environmentParam reads ?environment=sandbox|live of an app route. Absent means live, as these routes always have; anything else that is not one of the two
// is a 400. The console passes it explicitly and never relies on the default.
func environmentParam(r *http.Request) (env.Env, error) {
	v := r.URL.Query().Get("environment")
	if v == "" {
		return env.Live, nil
	}
	e, err := env.Parse(v)
	if err != nil {
		return "", invalid("environment", "sandbox or live")
	}
	return e, nil
}

// accountOf names the account a request is about: ?account_id= when given (it must be one of the session's), else the first. A person with no account
// cannot happen after sign-up, but is answered like any account that is not theirs.
func accountOf(r *http.Request, sess session) (string, error) {
	want := r.URL.Query().Get("account_id")
	for _, m := range sess.Memberships {
		if want == "" || m.GetAccountId() == want {
			return m.GetAccountId(), nil
		}
	}
	return "", errkey.New(codes.NotFound, "identity.account.not_found", "no such account")
}

// withAccount runs a handler of the signed-in person's account: the account is ?account_id= (default: the first one), checked against the session's
// memberships. The handler gets the session and the account's id.
func (s *Server) withAccount(h func(w http.ResponseWriter, r *http.Request, sess session, accountID string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		id, err := accountOf(r, sess)
		if err != nil {
			writeError(w, r, err)
			return
		}
		h(w, r, sess, id)
	}
}

func (s *Server) appAccount(w http.ResponseWriter, r *http.Request, sess session, id string) {
	resp, err := s.d.Identity.GetMyAccount(r.Context(), &plansv1.GetMyAccountRequest{UserId: sess.UserID, AccountId: id})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appList(w http.ResponseWriter, r *http.Request, sess session, id string) {
	resp, err := s.d.Identity.ListMyApps(r.Context(), &plansv1.ListMyAppsRequest{UserId: sess.UserID, AccountId: id})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appCreate(w http.ResponseWriter, r *http.Request, sess session, id string) {
	var body struct {
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Currency string `json:"currency"`
		Timezone string `json:"timezone"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.CreateMyApp(r.Context(), &plansv1.CreateMyAppRequest{
		UserId: sess.UserID, AccountId: id, Name: body.Name, Slug: body.Slug, Currency: body.Currency, Timezone: body.Timezone})
	respond(w, r, http.StatusCreated, resp.GetApp(), err)
}

func (s *Server) appGet(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.GetMyApp(r.Context(), &plansv1.GetMyAppRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appKeys(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	resp, err := s.d.Identity.ListMyApiKeys(r.Context(), &plansv1.ListMyApiKeysRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) appKeyCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Environment string `json:"environment"`
		// Role is admin (the default), billing, read_only, ingest_only or custom; Permissions are the permissions of a custom key (resource:action). Scope is the
		// older way to say the same ("full", "ingest" or "read") and is read when no role is given.
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
		Scope       string   `json:"scope"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	role := body.Role
	if role == "" && len(body.Permissions) == 0 {
		legacy, ok := apikey.LegacyScope(body.Scope)
		if !ok {
			writeError(w, r, invalid("scope", "full, ingest or read (or use role)"))
			return
		}
		role = legacy
	}
	if _, _, ok := apikey.Resolve(role, body.Permissions); !ok {
		writeError(w, r, invalid("role", "admin, billing, read_only, ingest_only or custom, with known permissions"))
		return
	}
	// The environment of a key is always named: there is no default, so a key is never made for live by leaving a field out.
	keyEnv, err := env.Parse(body.Environment)
	if err != nil {
		writeError(w, r, invalid("environment", "sandbox or live"))
		return
	}
	noStore(w)
	resp, err := s.d.Identity.CreateMyApiKey(r.Context(), &plansv1.CreateMyApiKeyRequest{UserId: sess.UserID, AppId: r.PathValue("id"), Environment: keyEnv.Proto(), Role: role, Permissions: body.Permissions})
	respond(w, r, http.StatusCreated, resp, err)
}

// appRename is PATCH /app/v1/apps/{id} {name} (owners and admins). The answer is the app.
func (s *Server) appRename(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Identity.RenameMyApp(r.Context(), &plansv1.RenameMyAppRequest{UserId: sess.UserID, AppId: r.PathValue("id"), Name: body.Name})
	respond(w, r, http.StatusOK, resp.GetApp(), err)
}

// appDelete is DELETE /app/v1/apps/{id} (owners only). The app is archived, never erased: its billing records stay. 204.
func (s *Server) appDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if _, err := s.d.Identity.DeleteMyApp(r.Context(), &plansv1.DeleteMyAppRequest{UserId: sess.UserID, AppId: r.PathValue("id")}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) appKeyRevoke(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	_, err := s.d.Identity.RevokeMyApiKey(r.Context(), &plansv1.RevokeMyApiKeyRequest{UserId: sess.UserID, AppId: r.PathValue("id"), ApiKeyId: r.PathValue("key_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
