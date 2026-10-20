// Package meters registers the meter handlers of PlansService: request to command or query, result to response.
package meters

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/meters/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/meters/query"
)

// Handler serves the meter methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the meter handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateMeter(ctx context.Context, req *plansv1.CreateMeterRequest) (*plansv1.CreateMeterResponse, error) {
	m, err := h.cmd.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateMeterResponse{Meter: m}, nil
}

func (h *Handler) GetMeter(ctx context.Context, req *plansv1.GetMeterRequest) (*plansv1.GetMeterResponse, error) {
	m, err := h.qry.Get(ctx, req.GetCode())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMeterResponse{Meter: m}, nil
}

func (h *Handler) ListMeters(ctx context.Context, _ *plansv1.ListMetersRequest) (*plansv1.ListMetersResponse, error) {
	ms, err := h.qry.List(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.ListMetersResponse{Meters: ms}, nil
}
