// Package providermigrations registers the provider migration handlers of PlansService: request to command or query, result to response.
package providermigrations

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/providermigrations/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/providermigrations/query"
)

// Handler serves the provider migration methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the provider migration handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) StartProviderMigration(ctx context.Context, req *plansv1.StartProviderMigrationRequest) (*plansv1.StartProviderMigrationResponse, error) {
	m, err := h.cmd.Start(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.StartProviderMigrationResponse{Migration: m}, nil
}

func (h *Handler) CompleteProviderMigration(ctx context.Context, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error) {
	return h.cmd.Complete(ctx, req)
}

func (h *Handler) AbortProviderMigration(ctx context.Context, req *plansv1.AbortProviderMigrationRequest) (*plansv1.AbortProviderMigrationResponse, error) {
	m, err := h.cmd.Abort(ctx, req.GetMigrationId())
	if err != nil {
		return nil, err
	}
	return &plansv1.AbortProviderMigrationResponse{Migration: m}, nil
}

func (h *Handler) GetProviderMigration(ctx context.Context, req *plansv1.GetProviderMigrationRequest) (*plansv1.GetProviderMigrationResponse, error) {
	m, err := h.qry.Get(ctx, req.GetMigrationId())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetProviderMigrationResponse{Migration: m}, nil
}
