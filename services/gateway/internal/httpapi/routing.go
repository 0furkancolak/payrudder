package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (s *Server) getRoutingRules(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetRoutingRules(r.Context(), &plansv1.GetRoutingRulesRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) setRoutingRules(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetRoutingRulesRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.SetRoutingRules(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getMailSettings(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetMailSettings(r.Context(), &plansv1.GetMailSettingsRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) setMailSettings(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetMailSettingsRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.SetMailSettings(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) previewRouting(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.PreviewRoutingRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.PreviewRouting(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
