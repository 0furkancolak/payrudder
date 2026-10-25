// Package rerating serves the re-rating methods of BillingAdminService: it maps requests to commands and queries.
package rerating

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rerating/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rerating/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
)

// Handler implements the re-rating methods of billingv1.BillingAdminServiceServer.
type Handler struct {
	runs *runs.Manager
}

// New returns the handler.
func New(mgr *runs.Manager) *Handler { return &Handler{runs: mgr} }

// invalid is an InvalidArgument status with the error key key.
func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

// StartRerating implements billingv1.BillingAdminServiceServer.
func (h *Handler) StartRerating(ctx context.Context, req *billingv1.StartReratingRequest) (*billingv1.StartReratingResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, invalid("billing.tenant.bad_id", "tenant_id must be a UUID")
	}
	pv, err := uuid.Parse(req.GetPlanVersionId())
	if err != nil {
		return nil, invalid("billing.rerating.bad_plan_version_id", "plan_version_id must be a UUID")
	}
	job, created, err := command.Start(ctx, h.runs, tid, req.GetPeriodId(), pv)
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
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, invalid("billing.tenant.bad_id", "tenant_id must be a UUID")
	}
	id, err := uuid.Parse(req.GetJobId())
	if err != nil {
		return nil, invalid("billing.rerating.bad_job_id", "job_id must be a UUID")
	}
	job, err := query.Get(ctx, h.runs, tid, id)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetReratingJobResponse{Job: reratingProto(job)}, nil
}

func reratingProto(j *runs.ReratingJob) *billingv1.ReratingJob {
	st := billingv1.ReratingStatus_RERATING_STATUS_UNSPECIFIED
	switch j.Status {
	case runs.ReratingRunning:
		st = billingv1.ReratingStatus_RERATING_STATUS_RUNNING
	case runs.ReratingCompleted:
		st = billingv1.ReratingStatus_RERATING_STATUS_COMPLETED
	case runs.ReratingFailed:
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
