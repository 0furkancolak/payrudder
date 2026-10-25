// Package promote is the module behind "promote sandbox configuration to live" (phase 45 D). IdentityService registers its one handler beside the others.
package promote

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote/command"
)

// Handler serves PromoteMyApp.
type Handler struct{ cmd *command.Commands }

// New returns the promotion handler.
func New(cmd *command.Commands) *Handler { return &Handler{cmd: cmd} }

func (h *Handler) PromoteMyApp(ctx context.Context, req *plansv1.PromoteMyAppRequest) (*plansv1.PromoteMyAppResponse, error) {
	return h.cmd.Promote(ctx, req)
}
