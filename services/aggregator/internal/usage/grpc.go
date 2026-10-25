// Package usage registers the usage read handlers of UsageQueryService: request to query, result to response.
package usage

import (
	"context"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/usage/query"
)

// Handler serves the usage methods of UsageQueryService.
type Handler struct{ qry *query.Queries }

// New returns the usage handlers.
func New(qry *query.Queries) *Handler { return &Handler{qry: qry} }

func (h *Handler) GetCustomerUsage(ctx context.Context, req *aggregatorv1.GetCustomerUsageRequest) (*aggregatorv1.GetCustomerUsageResponse, error) {
	rows, err := h.qry.CustomerUsage(ctx, req.GetCustomerId(), req.GetPeriodId(), req.GetGeneration())
	if err != nil {
		return nil, err
	}
	return &aggregatorv1.GetCustomerUsageResponse{Usage: rows}, nil
}

func (h *Handler) GetUsageBatch(ctx context.Context, req *aggregatorv1.GetUsageBatchRequest) (*aggregatorv1.GetUsageBatchResponse, error) {
	b, err := h.qry.UsageBatch(ctx, req.GetKeys(), req.GetGeneration())
	if err != nil {
		return nil, err
	}
	return &aggregatorv1.GetUsageBatchResponse{Usage: b.Usage, ConsumerOffsets: b.ConsumerOffsets}, nil
}

func (h *Handler) StreamPeriodUsage(req *aggregatorv1.StreamPeriodUsageRequest, stream aggregatorv1.UsageQueryService_StreamPeriodUsageServer) error {
	rows, err := h.qry.PeriodUsage(stream.Context(), req.GetPeriodId(), req.GetGeneration())
	if err != nil {
		return err
	}
	for _, u := range rows {
		if err := stream.Send(&aggregatorv1.StreamPeriodUsageResponse{Usage: u}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) StreamLateUsage(req *aggregatorv1.StreamLateUsageRequest, stream aggregatorv1.UsageQueryService_StreamLateUsageServer) error {
	rows, err := h.qry.LateUsage(stream.Context(), req.GetBilledPeriodId(), req.GetGeneration())
	if err != nil {
		return err
	}
	for _, u := range rows {
		if err := stream.Send(&aggregatorv1.StreamLateUsageResponse{LateUsage: u}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) GetIngestWatermark(ctx context.Context, req *aggregatorv1.GetIngestWatermarkRequest) (*aggregatorv1.GetIngestWatermarkResponse, error) {
	committed, caughtUp, err := h.qry.Watermark(ctx, req.GetRequiredOffsets())
	if err != nil {
		return nil, err
	}
	return &aggregatorv1.GetIngestWatermarkResponse{CaughtUp: caughtUp, CommittedOffsets: committed}, nil
}
