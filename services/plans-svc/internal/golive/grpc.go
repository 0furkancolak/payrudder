// Package golive is the module behind the go-live gate (phase 45): the checklist of a live environment and its activation. IdentityService registers
// these two handlers beside the others; they take the user id the gateway resolved and find the app only through that user's membership.
package golive

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/golive/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/golive/query"
)

// Handler serves the go-live calls of IdentityService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the go-live handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetMyGoLive(ctx context.Context, req *plansv1.GetMyGoLiveRequest) (*plansv1.GetMyGoLiveResponse, error) {
	g, err := h.qry.GoLive(ctx, req.GetUserId(), req.GetAppId(), req.GetProviderReady())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMyGoLiveResponse{GoLive: g}, nil
}

func (h *Handler) ActivateMyApp(ctx context.Context, req *plansv1.ActivateMyAppRequest) (*plansv1.ActivateMyAppResponse, error) {
	g, err := h.cmd.Activate(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ActivateMyAppResponse{GoLive: g}, nil
}
