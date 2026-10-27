package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	runmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	runquery "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// StartJob is Start without the mapping of its refusals to keyed errors: it creates the job for (tenant, period, plan version) and runs it. A repeated request returns the existing job (created=false) and finishes it if a crash
// left it RUNNING: the result is the same either way, one correction or credit per customer at most.
func (c *Commands) StartJob(ctx context.Context, tenantID uuid.UUID, periodID string, planVersionID uuid.UUID) (*model.Job, bool, error) {
	run, err := runquery.Load(ctx, c.q, tenantID, periodID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, model.ErrNoRun
	}
	if err != nil {
		return nil, false, err
	}
	if run.Status != runmodel.Completed {
		return nil, false, model.ErrRunNotCompleted
	}
	resp, err := c.cfg.Inputs.Plans.GetPlanVersion(inputs.TenantCtx(ctx, tenantID), &plansv1.GetPlanVersionRequest{PlanVersionId: planVersionID.String()})
	if err == nil && resp.GetPlanVersion() == nil {
		err = errors.New("not found")
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", model.ErrUnknownPlanVer, err)
	}
	gen := int32(1)
	if run.Generation != nil {
		gen = *run.Generation
	}
	inserted, err := c.q.InsertReratingJob(ctx, sqlc.InsertReratingJobParams{TenantID: tenantID, ID: uuid.New(), PeriodID: periodID, PlanVersionID: planVersionID, Generation: gen})
	if err != nil {
		return nil, false, err
	}
	row, err := c.q.GetReratingJobByPlanVersion(ctx, sqlc.GetReratingJobByPlanVersionParams{TenantID: tenantID, PeriodID: periodID, PlanVersionID: planVersionID})
	if err != nil {
		return nil, false, err
	}
	job, created := model.Of(row), inserted == 1
	if job.Status == model.Failed {
		// The cause may be gone (a plan version that was broken for a while): asking again runs the job again.
		if n, err := c.q.RestartReratingJob(ctx, sqlc.RestartReratingJobParams{TenantID: tenantID, ID: job.ID}); err != nil {
			return nil, false, err
		} else if n == 1 {
			job.Status, job.LastError = model.Running, ""
		}
	}
	if job.Status == model.Running {
		if job, err = c.rerate(ctx, job); err != nil {
			return job, created, err
		}
	}
	return job, created, nil
}

// Resume finishes jobs a crash left RUNNING (called by the scheduler loop).
func (c *Commands) Resume(ctx context.Context) error {
	rows, err := c.q.ListRunningReratingJobs(ctx, c.cfg.Now().Add(-time.Minute))
	if err != nil {
		return err
	}
	for _, r := range rows {
		j := model.Of(r)
		if _, err := c.rerate(ctx, j); err != nil {
			c.cfg.Log.WarnContext(ctx, "re-rating: job failed to finish", "tenant", j.TenantID, "job", j.ID, "error", err)
		}
	}
	return nil
}

// rerate does the work of a job. Everything it writes is keyed by the job, so running it again completes what a crash interrupted and creates nothing twice.
func (c *Commands) rerate(ctx context.Context, job *model.Job) (*model.Job, error) {
	if err := c.rerateOnce(ctx, job); err != nil {
		if inputs.IsPermanent(err) {
			if ferr := c.q.FailReratingJob(ctx, sqlc.FailReratingJobParams{LastError: err.Error(), TenantID: job.TenantID, ID: job.ID}); ferr != nil {
				// The job stays RUNNING and the next pass tries it again; say why it never reaches FAILED.
				c.cfg.Log.ErrorContext(ctx, "re-rating: job not marked failed", "tenant", job.TenantID, "job", job.ID, "error", ferr)
			}
		}
		return job, err
	}
	return query.Load(ctx, c.q, job.TenantID, job.ID)
}
