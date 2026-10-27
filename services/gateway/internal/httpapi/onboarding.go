package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// What the console remembers for the signed-in person, not for an account: how far they are in the first-run wizard and which page tours they have seen, so
// a second browser does not start them again. A session route only (an API key has no person), and the body never names the person.

func (s *Server) onboardingRoutes() []Route {
	return []Route{
		{"GET", "/app/v1/me/onboarding", AccessSession, ratelimit.ClassRead, s.myOnboarding},
		{"PATCH", "/app/v1/me/onboarding", AccessSession, ratelimit.ClassWrite, s.updateMyOnboarding},
	}
}

func (s *Server) myOnboarding(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Identity.GetMyOnboarding(r.Context(), &plansv1.GetMyOnboardingRequest{UserId: sessionFrom(r.Context()).UserID})
	respond(w, r, http.StatusOK, resp.GetOnboarding(), err)
}

// updateMyOnboarding merges the change into the stored record (plans-svc says how) and answers with the result.
func (s *Server) updateMyOnboarding(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.UpdateMyOnboardingRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.UserId = sessionFrom(r.Context()).UserID
	resp, err := s.d.Identity.UpdateMyOnboarding(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetOnboarding(), err)
}
