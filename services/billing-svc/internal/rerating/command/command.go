// Package command starts re-rating jobs for the operator and finishes the ones a crash left running. A job prices a closed period again under another plan
// version and bills the difference: a correction invoice for a customer who now owes more, a credit balance for one who is owed.
package command

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Config is what the commands run on.
type Config struct {
	DB       *tenantdb.DB // billing_system
	Inputs   *inputs.Inputs
	Invoices *invoicecmd.Commands
	Now      func() time.Time
	Log      *slog.Logger
}

// Commands are the re-rating commands.
type Commands struct {
	cfg Config
	q   *sqlc.Queries // only for WithTx: the statements run in the transactions DB opens
}

// New returns the commands; a missing clock is the wall clock and a missing logger the default one.
func New(cfg Config) *Commands {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Commands{cfg: cfg, q: sqlc.New(cfg.DB.Pool())}
}

// Start starts, or finds, the re-rating job of a period under a plan version. created says whether it is new.
// A job that exists but could not be driven now is returned without an error: the scheduler finishes it.
func (c *Commands) Start(ctx context.Context, tenantID uuid.UUID, periodID string, planVersionID uuid.UUID) (job *model.Job, created bool, err error) {
	job, created, err = c.StartJob(ctx, tenantID, periodID, planVersionID)
	switch {
	case errors.Is(err, model.ErrNoRun):
		return nil, false, errkey.New(codes.NotFound, "billing.run.not_found", "no billing run for that period")
	case errors.Is(err, model.ErrRunNotCompleted):
		return nil, false, errkey.New(codes.FailedPrecondition, "billing.rerating.run_not_completed", "the billing run of that period is not COMPLETED")
	case errors.Is(err, model.ErrUnknownPlanVer):
		return nil, false, errkey.New(codes.InvalidArgument, "billing.rerating.unknown_plan_version", "plan_version_id: no such plan version")
	case err != nil && job != nil:
		return job, created, nil
	case err != nil:
		c.cfg.Log.ErrorContext(ctx, "start rerating", "error", err)
		return nil, false, errkey.New(codes.Unavailable, "billing.rerating.unavailable", "re-rating could not start; retry")
	}
	return job, created, nil
}
