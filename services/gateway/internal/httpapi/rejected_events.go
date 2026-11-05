package httpapi

import (
	"net/http"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// rejectedEventRoutes is the table for the log of rejected events (docs/plans/18-quick-integration.md): what POST /v1/events refused, kept for 14 days. They read, so a key needs
// usage:read; the console reaches them through the tenant API like the rest, and a viewer may read.
func (s *Server) rejectedEventRoutes() []Route {
	return []Route{
		{"GET", "/v1/rejected-events", AccessTenant, ratelimit.ClassRead, s.listRejectedEvents},
		{"GET", "/v1/rejected-events/summary", AccessTenant, ratelimit.ClassRead, s.rejectedEventSummary},
	}
}

// listRejectedEvents is GET /v1/rejected-events?error_code=&meter=&page_size=&page_token=: newest first.
func (s *Server) listRejectedEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n, err := intParam(q, "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Ingest.ListRejectedEvents(r.Context(), &ingestv1.ListRejectedEventsRequest{
		ErrorCode: q.Get("error_code"), MeterCode: q.Get("meter"), PageSize: n, PageToken: q.Get("page_token"),
	})
	respond(w, r, http.StatusOK, resp, err)
}

// rejectedEventSummary is GET /v1/rejected-events/summary: the last 24 hours by error code and by hour.
func (s *Server) rejectedEventSummary(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Ingest.GetRejectedEventSummary(r.Context(), &ingestv1.GetRejectedEventSummaryRequest{})
	respond(w, r, http.StatusOK, resp, err)
}
