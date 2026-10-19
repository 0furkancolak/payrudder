package httpapi

import (
	"net/http"

	aggregatorv1 "github.com/0furkancolak/quotify/gen/go/quotify/aggregator/v1"
)

// Replay admin routes: rebuild one tenant's aggregates into a new generation, verify the difference, promote or discard
// (docs/07-correctness.md#8-replay--re-rating). Operators only.

func (s *Server) adminStartReplay(w http.ResponseWriter, r *http.Request) {
	req := &aggregatorv1.StartReplayRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.TenantId = r.PathValue("tenant_id")
	resp, err := s.d.Replay.StartReplay(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetJob(), err)
}

func (s *Server) adminGetReplay(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Replay.GetReplayJob(r.Context(), &aggregatorv1.GetReplayJobRequest{TenantId: r.PathValue("tenant_id"), JobId: r.PathValue("job_id")})
	respond(w, r, http.StatusOK, resp.GetJob(), err)
}

func (s *Server) adminPromoteReplay(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Replay.PromoteGeneration(r.Context(), &aggregatorv1.PromoteGenerationRequest{TenantId: r.PathValue("tenant_id"), JobId: r.PathValue("job_id")})
	respond(w, r, http.StatusOK, resp.GetJob(), err)
}

func (s *Server) adminDiscardReplay(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Replay.DiscardGeneration(r.Context(), &aggregatorv1.DiscardGenerationRequest{TenantId: r.PathValue("tenant_id"), JobId: r.PathValue("job_id")})
	respond(w, r, http.StatusOK, resp.GetJob(), err)
}
