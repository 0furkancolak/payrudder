// Package periods registers the period handlers of UsageQueryService: request to command, result to response.
package periods

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/periods/command"
)

// Handler serves the period methods of UsageQueryService.
type Handler struct{ cmd *command.Commands }

// New returns the period handlers.
func New(cmd *command.Commands) *Handler { return &Handler{cmd: cmd} }

func (h *Handler) FreezePeriod(ctx context.Context, req *aggregatorv1.FreezePeriodRequest) (*aggregatorv1.FreezePeriodResponse, error) {
	at, err := h.cmd.Freeze(ctx, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	resp := &aggregatorv1.FreezePeriodResponse{}
	if at != nil {
		resp.FrozenAt = timestamppb.New(*at)
	}
	return resp, nil
}

func (h *Handler) ClosePeriodEarly(ctx context.Context, req *aggregatorv1.ClosePeriodEarlyRequest) (*aggregatorv1.ClosePeriodEarlyResponse, error) {
	at, err := h.cmd.CloseEarly(ctx, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	return &aggregatorv1.ClosePeriodEarlyResponse{CutoffAt: timestamppb.New(at)}, nil
}
