package httpapi

import (
	"net/http"
	"strconv"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (s *Server) createMeter(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateMeterRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateMeter(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetMeter(), err)
}

func (s *Server) listMeters(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListMeters(r.Context(), &plansv1.ListMetersRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getMeter(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetMeter(r.Context(), &plansv1.GetMeterRequest{Code: r.PathValue("code")})
	respond(w, r, http.StatusOK, resp.GetMeter(), err)
}

func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreatePlanRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreatePlan(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetPlan(), err)
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListPlans(r.Context(), &plansv1.ListPlansRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) publishPlanVersion(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.PublishPlanVersionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.PlanCode = r.PathValue("code")
	resp, err := s.d.Plans.PublishPlanVersion(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetPlanVersion(), err)
}

func (s *Server) getPlanVersion(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.GetPlanVersionRequest{PlanCode: r.PathValue("code")}
	if v := r.PathValue("version"); v != "latest" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 {
			writeError(w, r, invalid("version", `a positive integer or "latest"`))
			return
		}
		req.Version = int32(n)
	}
	resp, err := s.d.Plans.GetPlanVersion(r.Context(), req)
	if err == nil && req.Version != 0 {
		// A published version never changes: the caller (or an edge in front) may keep it for a while without asking. Only an answer that found it:
		// the "not found" of a version that is published a moment later must not be kept.
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	respond(w, r, http.StatusOK, resp.GetPlanVersion(), err)
}
