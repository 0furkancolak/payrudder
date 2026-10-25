package webhook

import (
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/env"
)

// goLive answers the one go-live question that payments-worker owns (phase 45): does the tenant have a payment provider connected in live mode that works?
// A provider counts when the tenant has a live-mode account of it whose stored settings still open and still build an adapter (the same check that stores
// them, so a rotated secret key or a broken record shows up here). The gateway asks this when it computes the checklist; plans-svc never sees provider data.
func (s *Server) goLive(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	list, err := s.Accounts.List(r.Context(), t)
	if err != nil {
		s.Log.ErrorContext(r.Context(), "go-live: accounts not listed", "err", err)
		fail(w, http.StatusInternalServerError, "common.internal", "internal error")
		return
	}
	working := []string{}
	for _, a := range list {
		if a.Environment != env.Live.String() {
			continue
		}
		settings, _, err := s.Accounts.Settings(r.Context(), t, a.Provider, a.Environment)
		if err != nil {
			continue
		}
		if _, err := s.Registry.Build(a.Provider, settings); err != nil {
			continue
		}
		working = append(working, a.Provider)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": len(working) > 0, "providers": working})
}
