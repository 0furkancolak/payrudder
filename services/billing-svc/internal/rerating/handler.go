// Package rerating serves the re-rating methods of BillingAdminService: it maps requests to commands and queries.
package rerating

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jackc/pgx/v5/pgxpool"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Handler implements the re-rating methods of billingv1.BillingAdminServiceServer.
type Handler struct {
	cmd *command.Commands
	q   *sqlc.Queries
}

// New returns the handler.
func New(cmd *command.Commands, system *pgxpool.Pool) *Handler {
	return &Handler{cmd: cmd, q: sqlc.New(system)}
}

// StartRerating implements billingv1.BillingAdminServiceServer.
func (h *Handler) StartRerating(ctx context.Context, req *billingv1.StartReratingRequest) (*billingv1.StartReratingResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	pv, err := errkey.ParseUUID("billing.rerating.bad_plan_version_id", "plan_version_id", req.GetPlanVersionId())
	if err != nil {
		return nil, err
	}
	job, created, err := h.cmd.Start(ctx, tid, req.GetPeriodId(), pv)
	if err != nil {
		return nil, err
	}
	return &billingv1.StartReratingResponse{Job: reratingProto(job), Created: created}, nil
}

// GetReratingJob implements billingv1.BillingAdminServiceServer.
func (h *Handler) GetReratingJob(ctx context.Context, req *billingv1.GetReratingJobRequest) (*billingv1.GetReratingJobResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	id, err := errkey.ParseUUID("billing.rerating.bad_job_id", "job_id", req.GetJobId())
	if err != nil {
		return nil, err
	}
	job, err := query.Get(ctx, h.q, tid, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetReratingJobResponse{Job: reratingProto(job)}, nil
}

func reratingProto(j *model.Job) *billingv1.ReratingJob {
	st := billingv1.ReratingStatus_RERATING_STATUS_UNSPECIFIED
	switch j.Status {
	case model.Running:
		st = billingv1.ReratingStatus_RERATING_STATUS_RUNNING
	case model.Completed:
		st = billingv1.ReratingStatus_RERATING_STATUS_COMPLETED
	case model.Failed:
		st = billingv1.ReratingStatus_RERATING_STATUS_FAILED
	}
	out := &billingv1.ReratingJob{
		Id: j.ID.String(), TenantId: j.TenantID.String(), PeriodId: j.PeriodID, PlanVersionId: j.PlanVersionID.String(), Generation: j.Generation,
		Status: st, CustomersRated: int32(j.CustomersRated), Corrections: int32(j.Corrections), Credits: int32(j.Credits), //nolint:gosec // counts of one tenant's customers
		CorrectionTotalMinor: j.CorrectionMinor, CreditTotalMinor: j.CreditMinor, LastError: j.LastError, CreateTime: timestamppb.New(j.CreatedAt),
	}
	if j.CompletedAt != nil {
		out.CompleteTime = timestamppb.New(*j.CompletedAt)
	}
	return out
}
