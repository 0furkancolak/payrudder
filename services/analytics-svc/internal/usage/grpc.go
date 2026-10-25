// Package usage registers the usage handlers of AnalyticsService: request to query, result to response.
package usage

import (
	"context"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/usage/query"
)

// Handler serves the methods of AnalyticsService.
type Handler struct{ qry *query.Queries }

// New returns the usage handlers.
func New(qry *query.Queries) *Handler { return &Handler{qry: qry} }

func (h *Handler) QueryUsage(ctx context.Context, req *analyticsv1.QueryUsageRequest) (*analyticsv1.QueryUsageResponse, error) {
	return h.qry.Series(ctx, req)
}

func (h *Handler) TopCustomers(ctx context.Context, req *analyticsv1.TopCustomersRequest) (*analyticsv1.TopCustomersResponse, error) {
	return h.qry.TopCustomers(ctx, req)
}

func (h *Handler) GetIngestStats(ctx context.Context, req *analyticsv1.GetIngestStatsRequest) (*analyticsv1.GetIngestStatsResponse, error) {
	return h.qry.IngestStats(ctx, req)
}
