package api

import (
	"context"
	"regexp"
	"slices"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

var periodRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// revenueSplits are what a revenue report can be split by. Currency is always split.
var revenueSplits = []string{"period", "provider", "country"}

// GetRevenueReport implements billingv1.BillingServiceServer.
func (s *Server) GetRevenueReport(ctx context.Context, req *billingv1.GetRevenueReportRequest) (*billingv1.GetRevenueReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range []string{req.GetFromPeriod(), req.GetToPeriod()} {
		if p != "" && !periodRe.MatchString(p) {
			return nil, invalid("from_period and to_period must look like 2026-10")
		}
	}
	if req.GetFromPeriod() != "" && req.GetToPeriod() != "" && req.GetFromPeriod() > req.GetToPeriod() {
		return nil, invalid("from_period is after to_period")
	}
	groups := req.GetGroupBy()
	if len(groups) == 0 {
		groups = []string{"period"}
	}
	for _, g := range groups {
		if !slices.Contains(revenueSplits, g) {
			return nil, invalid("group_by may name period, provider and country; got " + g)
		}
	}
	// A refunded invoice is one whose saga passed through REFUNDED, whatever its status is now (voiding a paid invoice refunds it first).
	var rows []sqlc.RevenueReportRow
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).RevenueReport(ctx, sqlc.RevenueReportParams{
			ByPeriod: slices.Contains(groups, "period"), ByProvider: slices.Contains(groups, "provider"), ByCountry: slices.Contains(groups, "country"),
			FromPeriod: store.NilIfEmpty(req.GetFromPeriod()), ToPeriod: store.NilIfEmpty(req.GetToPeriod()),
		})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	resp := &billingv1.GetRevenueReportResponse{}
	for _, r := range rows {
		resp.Rows = append(resp.Rows, &billingv1.RevenueRow{
			Period: r.Period, Provider: r.Provider, Country: r.Country, Currency: r.Currency, Invoices: r.Invoices, BilledMinor: r.BilledMinor,
			CollectedMinor: r.CollectedMinor, RefundedMinor: r.RefundedMinor, OutstandingMinor: r.OutstandingMinor, UncollectibleMinor: r.UncollectibleMinor,
			VoidMinor: r.VoidMinor, CreditedMinor: r.CreditedMinor,
		})
	}
	return resp, nil
}

// GetDunningReport implements billingv1.BillingServiceServer.
func (s *Server) GetDunningReport(ctx context.Context, req *billingv1.GetDunningReportRequest) (*billingv1.GetDunningReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	switch {
	case limit == 0:
		limit = 100
	case limit < 0 || limit > 500:
		return nil, invalid("limit must be 1..500")
	}
	resp := &billingv1.GetDunningReportResponse{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		summary, err := q.DunningSummary(ctx)
		if err != nil {
			return err
		}
		for _, d := range summary {
			resp.Summary = append(resp.Summary, &billingv1.DunningSummary{State: d.State, Provider: d.Provider, Currency: d.Currency, Invoices: d.Invoices, TotalMinor: d.TotalMinor})
		}
		invoices, err := q.DunningInvoices(ctx, int32(limit)) //nolint:gosec // at most 500
		if err != nil {
			return err
		}
		for _, d := range invoices {
			inv := &billingv1.DunningInvoice{InvoiceId: d.InvoiceID, Number: d.Number, CustomerExternalId: d.CustomerExternalID, Period: d.PeriodID, Provider: d.Provider,
				Currency: d.Currency, TotalMinor: d.TotalMinor, State: d.State, DunningAttempt: d.DunningAttempt, LastError: d.LastError}
			if d.NextAttemptAt != nil {
				inv.NextAttemptTime = timestamppb.New(*d.NextAttemptAt)
			}
			resp.Invoices = append(resp.Invoices, inv)
		}
		return nil
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return resp, nil
}
