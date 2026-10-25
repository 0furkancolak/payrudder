package httpapi

import (
	"net/http"
	"strings"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// E-mail verification and password reset (phase 39). The routes are public: the secret is the token in the mailed link, 256 random bits that plans-svc looks up
// by hash and spends once. The gateway adds the per-address limits and never says whether an address has an account.

// Per-address limits per minute: "forgot password" sends mail, so it is the tightest; spending a token is cheap but is not something a person does often.
const (
	forgotsPerMinute    = 5
	tokenUsesPerMinute  = 20
	maxAcceptLanguageLn = 200
)

// supportedLocales are the languages of the console and of the mail.
var supportedLocales = []string{"en", "tr", "de", "fr", "es", "ar"}

// localeOf picks the language for a mail: the one the page sent when it is supported, else the first supported language of Accept-Language, else English.
func localeOf(r *http.Request, asked string) string {
	if l := matchLocale(asked); l != "" {
		return l
	}
	h := r.Header.Get("Accept-Language")
	if len(h) > maxAcceptLanguageLn {
		h = h[:maxAcceptLanguageLn]
	}
	for _, part := range strings.Split(h, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		if l := matchLocale(tag); l != "" {
			return l
		}
	}
	return "en"
}

func matchLocale(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	base, _, _ := strings.Cut(tag, "-")
	for _, l := range supportedLocales {
		if base == l {
			return l
		}
	}
	return ""
}

// verifyEmail is POST /auth/verify {token}: the link in the verification mail. The answer is the user, now verified.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.tokenUses, tokenUsesPerMinute) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	resp, err := s.d.Identity.VerifyEmail(r.Context(), &plansv1.VerifyEmailRequest{Token: body.Token})
	respond(w, r, http.StatusOK, resp, err)
}

// resendVerification is POST /auth/resend-verification: a new link for the signed-in person (plans-svc allows one a minute).
func (s *Server) resendVerification(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if _, err := s.d.Identity.ResendVerification(r.Context(), &plansv1.ResendVerificationRequest{UserId: sess.UserID}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forgotPassword is POST /auth/forgot {email}: always 202 for a well-formed address, whether or not it has an account.
func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.resets, forgotsPerMinute) {
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
	if _, err := s.d.Identity.RequestPasswordReset(r.Context(), &plansv1.RequestPasswordResetRequest{Email: body.Email, Locale: localeOf(r, body.Locale)}); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

// resetPassword is POST /auth/reset {token, password}: sets the new password and signs the person out everywhere. It opens no session; the person signs in.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.authAllowed(w, r, &s.tokenUses, tokenUsesPerMinute) {
		return
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	noStore(w)
	if _, err := s.d.Identity.ResetPassword(r.Context(), &plansv1.ResetPasswordRequest{Token: body.Token, NewPassword: body.Password}); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
