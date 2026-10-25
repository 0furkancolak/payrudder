// Package api registers the two sandbox calls of IdentityService (ResetMySandbox, GetMySandboxReset) and wires the module: the commands, the queries and the runner
// that executes the jobs.
package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox/query"
)

// Handler serves ResetMySandbox and GetMySandboxReset.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// Wire builds the handler and the runner. remotes are the steps after plans-svc's own, in the order they run; cooldown is the least time between two manual resets of a
// sandbox; set holds the runner's timings and the retention.
func Wire(systemPool *pgxpool.Pool, remotes []sandbox.Step, cooldown time.Duration, set sandbox.Settings, log *slog.Logger) (*Handler, *sandbox.Runner) {
	steps := append([]sandbox.Step{sandbox.NewLocal(systemPool).Step()}, remotes...)
	return &Handler{cmd: command.New(systemPool, sandbox.Names(steps), cooldown, log), qry: query.New(systemPool)}, sandbox.NewRunner(systemPool, steps, set, log)
}

func (h *Handler) ResetMySandbox(ctx context.Context, req *plansv1.ResetMySandboxRequest) (*plansv1.ResetMySandboxResponse, error) {
	r, err := h.cmd.Request(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ResetMySandboxResponse{SandboxReset: r}, nil
}

func (h *Handler) GetMySandboxReset(ctx context.Context, req *plansv1.GetMySandboxResetRequest) (*plansv1.GetMySandboxResetResponse, error) {
	r, err := h.qry.Latest(ctx, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMySandboxResetResponse{SandboxReset: r}, nil
}
