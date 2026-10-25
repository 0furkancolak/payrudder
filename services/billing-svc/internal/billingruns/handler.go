// Package billingruns serves the billing-run methods of SandboxService and BillingAdminService: it maps requests to commands and queries.
package billingruns

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	runcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/command"
	runmodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/query"
)

// Handler implements the billing-run methods of billingv1.SandboxServiceServer and billingv1.BillingAdminServiceServer.
type Handler struct {
	runs *runcmd.Manager
}

// New returns the handler.
func New(mgr *runcmd.Manager) *Handler { return &Handler{runs: mgr} }

// ClosePeriodNow implements billingv1.SandboxServiceServer.
func (h *Handler) ClosePeriodNow(ctx context.Context, req *billingv1.ClosePeriodNowRequest) (*billingv1.ClosePeriodNowResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	run, err := h.runs.ClosePeriodNow(ctx, ti, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	return &billingv1.ClosePeriodNowResponse{Run: runProto(run, run.InvoiceCount)}, nil
}

// ClosePeriod implements billingv1.BillingAdminServiceServer.
func (h *Handler) ClosePeriod(ctx context.Context, req *billingv1.ClosePeriodRequest) (*billingv1.ClosePeriodResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	run, err := h.runs.ClosePeriod(ctx, tid, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	return &billingv1.ClosePeriodResponse{Run: runProto(run, run.InvoiceCount)}, nil
}

// GetBillingRun implements billingv1.BillingAdminServiceServer.
func (h *Handler) GetBillingRun(ctx context.Context, req *billingv1.GetBillingRunRequest) (*billingv1.GetBillingRunResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := errkey.ParseUUID("billing.tenant.bad_id", "tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	run, err := query.Get(ctx, h.runs, tid, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	return &billingv1.GetBillingRunResponse{Run: runProto(run, run.InvoiceCount)}, nil
}

func runProto(r *runmodel.Run, invoices int) *billingv1.BillingRun {
	out := &billingv1.BillingRun{
		TenantId: r.TenantID.String(), PeriodId: r.PeriodID, CutoffTime: timestamppb.New(r.CutoffAt), InvoiceCount: int32(invoices), //nolint:gosec // small
		LastError: r.LastError,
	}
	switch r.Status {
	case runmodel.Scheduled:
		out.Status = billingv1.RunStatus_RUN_STATUS_SCHEDULED
	case runmodel.WaitingWatermark:
		out.Status = billingv1.RunStatus_RUN_STATUS_WAITING_WATERMARK
	case runmodel.Rating:
		out.Status = billingv1.RunStatus_RUN_STATUS_RATING
	case runmodel.Invoicing:
		out.Status = billingv1.RunStatus_RUN_STATUS_INVOICING
	case runmodel.Completed:
		out.Status = billingv1.RunStatus_RUN_STATUS_COMPLETED
	case runmodel.Failed:
		out.Status = billingv1.RunStatus_RUN_STATUS_FAILED
	}
	if r.Generation != nil {
		out.Generation = *r.Generation
	}
	if r.StartedAt != nil {
		out.StartTime = timestamppb.New(*r.StartedAt)
	}
	if r.CompletedAt != nil {
		out.CompleteTime = timestamppb.New(*r.CompletedAt)
	}
	return out
}
