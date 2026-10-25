// Package mailsettings registers the mail switch handlers of PlansService: request to command or query, result to response.
package mailsettings

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/query"
)

// Handler serves the mail settings methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the mail settings handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetMailSettings(ctx context.Context, _ *plansv1.GetMailSettingsRequest) (*plansv1.GetMailSettingsResponse, error) {
	cats, err := h.qry.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMailSettingsResponse{Categories: cats}, nil
}

func (h *Handler) SetMailSettings(ctx context.Context, req *plansv1.SetMailSettingsRequest) (*plansv1.SetMailSettingsResponse, error) {
	cats, err := h.cmd.Set(ctx, req.GetCategories())
	if err != nil {
		return nil, err
	}
	return &plansv1.SetMailSettingsResponse{Categories: cats}, nil
}
