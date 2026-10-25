// Package outbox registers the outbox handlers of IngestService: request to query, result to response.
package outbox

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/outbox/query"
)

// Handler serves the outbox methods of IngestService.
type Handler struct{ qry *query.Queries }

// New returns the outbox handlers.
func New(qry *query.Queries) *Handler { return &Handler{qry: qry} }

// GetOutboxWatermark implements ingestv1.IngestServiceServer.
func (h *Handler) GetOutboxWatermark(ctx context.Context, _ *ingestv1.GetOutboxWatermarkRequest) (*ingestv1.GetOutboxWatermarkResponse, error) {
	oldest, ok, err := h.qry.Watermark(ctx)
	if err != nil {
		return nil, err
	}
	resp := &ingestv1.GetOutboxWatermarkResponse{ServerNow: timestamppb.Now()}
	if ok {
		resp.OldestUnpublishedIngestedAt = timestamppb.New(oldest)
	}
	return resp, nil
}
