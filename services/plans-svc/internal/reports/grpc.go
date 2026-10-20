// Package reports registers the report handlers of PlansService: request to query, result to response. Reports only read.
package reports

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/reports/query"
)

// Handler serves the report methods of PlansService.
type Handler struct{ qry *query.Queries }

// New returns the report handlers.
func New(qry *query.Queries) *Handler { return &Handler{qry: qry} }

func (h *Handler) GetSubscriptionReport(ctx context.Context, req *plansv1.GetSubscriptionReportRequest) (*plansv1.GetSubscriptionReportResponse, error) {
	return h.qry.Subscriptions(ctx, req)
}
