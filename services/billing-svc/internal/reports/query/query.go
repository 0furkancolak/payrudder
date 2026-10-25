// Package query builds the revenue and dunning reports of a tenant. It never writes.
package query

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// revenueSplits are what a revenue report can be split by. Currency is always split.
var revenueSplits = []string{"period", "provider", "country"}

// RevenueParams bound and split a revenue report. Empty periods are open ends; no GroupBy means by period.
type RevenueParams struct {
	FromPeriod string
	ToPeriod   string
	GroupBy    []string
}

// Revenue returns the revenue rows of a tenant.
func Revenue(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, p RevenueParams) ([]sqlc.RevenueReportRow, error) {
	for _, id := range []string{p.FromPeriod, p.ToPeriod} {
		if id != "" && !period.Valid(id) {
			return nil, errkey.New(codes.InvalidArgument, "billing.report.bad_period", "from_period and to_period must look like 2026-10")
		}
	}
	if p.FromPeriod != "" && p.ToPeriod != "" && p.FromPeriod > p.ToPeriod {
		return nil, errkey.New(codes.InvalidArgument, "billing.report.period_order", "from_period is after to_period")
	}
	groups := p.GroupBy
	if len(groups) == 0 {
		groups = []string{"period"}
	}
	for _, g := range groups {
		if !slices.Contains(revenueSplits, g) {
			return nil, errkey.New(codes.InvalidArgument, "billing.report.bad_group_by", "group_by may name period, provider and country; got "+g)
		}
	}
	// A refunded invoice is one whose saga passed through REFUNDED, whatever its status is now (voiding a paid invoice refunds it first).
	var rows []sqlc.RevenueReportRow
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).RevenueReport(ctx, sqlc.RevenueReportParams{
			ByPeriod: slices.Contains(groups, "period"), ByProvider: slices.Contains(groups, "provider"), ByCountry: slices.Contains(groups, "country"),
			FromPeriod: store.NilIfEmpty(p.FromPeriod), ToPeriod: store.NilIfEmpty(p.ToPeriod),
		})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return rows, nil
}

// DunningReport is the summary of invoices in dunning and the invoices themselves.
type DunningReport struct {
	Summary  []sqlc.DunningSummaryRow
	Invoices []sqlc.DunningInvoicesRow
}

// Dunning returns the dunning report of a tenant; limit 0 means the default.
func Dunning(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, limit int) (*DunningReport, error) {
	switch {
	case limit == 0:
		limit = 100
	case limit < 0 || limit > 500:
		return nil, errkey.New(codes.InvalidArgument, "billing.report.bad_limit", "limit must be 1..500")
	}
	out := &DunningReport{}
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var err error
		if out.Summary, err = q.DunningSummary(ctx); err != nil {
			return err
		}
		out.Invoices, err = q.DunningInvoices(ctx, int32(limit)) //nolint:gosec // at most 500
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return out, nil
}
