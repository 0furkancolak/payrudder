// Package reconcile registers the reconciliation handler of LimitsService: request to command, result to response.
package reconcile

import (
	"context"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/reconcile/command"
)

// Handler serves ReconcileNow.
type Handler struct{ cmd *command.Commands }

// New returns the reconcile handler.
func New(cmd *command.Commands) *Handler { return &Handler{cmd: cmd} }

func (h *Handler) ReconcileNow(ctx context.Context, _ *limitsv1.ReconcileNowRequest) (*limitsv1.ReconcileNowResponse, error) {
	res, err := h.cmd.Run(ctx)
	if err != nil {
		return nil, err
	}
	return &limitsv1.ReconcileNowResponse{KeysChecked: res.Checked, Corrections: res.Corrections, PartitionsSkipped: res.Skipped}, nil
}
