package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// sandboxReset deletes a sandbox tenant's payment attempts, ledger and receipts (phase 45 E). The caller is plans-svc, behind the internal token; the body must
// say the environment is the sandbox, and anything else is refused here too.
func (s *Server) sandboxReset(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenant(w, r)
	if !ok {
		return
	}
	if s.Reset == nil {
		fail(w, http.StatusServiceUnavailable, errkey.Key("common.unavailable"), "sandbox reset is not configured")
		return
	}
	var in struct {
		Environment string `json:"environment"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, errkey.Key("common.invalid_argument"), "body: {\"environment\":\"sandbox\"}")
		return
	}
	if e, err := env.Parse(in.Environment); err != nil || !e.IsSandbox() {
		fail(w, http.StatusConflict, errkey.Key("admin.sandbox.live_refused"), "only a sandbox tenant can be reset")
		return
	}
	deleted, err := s.Reset(r.Context(), t)
	if err != nil {
		s.Log.ErrorContext(r.Context(), "sandbox reset failed", "tenant_id", t, "err", err)
		fail(w, http.StatusInternalServerError, errkey.Key("common.internal"), "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// ResetFunc deletes a sandbox tenant's data and reports the rows by table.
type ResetFunc func(ctx context.Context, tenant uuid.UUID) (map[string]int64, error)
