// Package status registers the limit status handlers of LimitsService: request to query, result to response.
package status

import (
	"context"

	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	"github.com/0furkancolak/payrudder/services/limits-svc/internal/status/query"
)

// Handler serves the status methods of LimitsService.
type Handler struct{ qry *query.Queries }

// New returns the status handlers.
func New(qry *query.Queries) *Handler { return &Handler{qry: qry} }

func (h *Handler) CheckEntitlement(ctx context.Context, req *limitsv1.CheckEntitlementRequest) (*limitsv1.CheckEntitlementResponse, error) {
	st, err := h.qry.Check(ctx, req.GetCustomerId(), req.GetMeterCode(), req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	return &limitsv1.CheckEntitlementResponse{Status: st}, nil
}

func (h *Handler) GetLimitStatus(ctx context.Context, req *limitsv1.GetLimitStatusRequest) (*limitsv1.GetLimitStatusResponse, error) {
	sts, err := h.qry.List(ctx, req.GetCustomerId(), req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	return &limitsv1.GetLimitStatusResponse{Statuses: sts}, nil
}
