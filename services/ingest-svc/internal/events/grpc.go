// Package events registers the IngestEvents handler of IngestService: request to command, result to response.
package events

import (
	"context"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/events/command"
)

// Handler serves the event methods of IngestService.
type Handler struct{ cmd *command.Commands }

// New returns the event handlers.
func New(cmd *command.Commands) *Handler { return &Handler{cmd: cmd} }

// IngestEvents implements ingestv1.IngestServiceServer.
func (h *Handler) IngestEvents(ctx context.Context, req *ingestv1.IngestEventsRequest) (*ingestv1.IngestEventsResponse, error) {
	results, err := h.cmd.Ingest(ctx, req.GetEvents())
	if err != nil {
		return nil, err
	}
	return &ingestv1.IngestEventsResponse{Results: results}, nil
}
