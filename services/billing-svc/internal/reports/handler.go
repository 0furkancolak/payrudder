// Package reports serves the report methods of BillingService: it maps requests to queries. Reports only read.
package reports

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/reports/query"
)

// Handler implements the report methods of billingv1.BillingServiceServer.
type Handler struct {
	app *pgxpool.Pool
}

// New returns the handler over the billing_app pool.
func New(app *pgxpool.Pool) *Handler { return &Handler{app: app} }

// GetRevenueReport implements billingv1.BillingServiceServer.
func (h *Handler) GetRevenueReport(ctx context.Context, req *billingv1.GetRevenueReportRequest) (*billingv1.GetRevenueReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := query.Revenue(ctx, h.app, ti.ID, query.RevenueParams{FromPeriod: req.GetFromPeriod(), ToPeriod: req.GetToPeriod(), GroupBy: req.GetGroupBy()})
	if err != nil {
		return nil, err
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
func (h *Handler) GetDunningReport(ctx context.Context, req *billingv1.GetDunningReportRequest) (*billingv1.GetDunningReportResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	report, err := query.Dunning(ctx, h.app, ti.ID, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	resp := &billingv1.GetDunningReportResponse{}
	for _, d := range report.Summary {
		resp.Summary = append(resp.Summary, &billingv1.DunningSummary{State: d.State, Provider: d.Provider, Currency: d.Currency, Invoices: d.Invoices, TotalMinor: d.TotalMinor})
	}
	for _, d := range report.Invoices {
		inv := &billingv1.DunningInvoice{InvoiceId: d.InvoiceID, Number: d.Number, CustomerExternalId: d.CustomerExternalID, Period: d.PeriodID, Provider: d.Provider,
			Currency: d.Currency, TotalMinor: d.TotalMinor, State: d.State, DunningAttempt: d.DunningAttempt, LastError: d.LastError}
		if d.NextAttemptAt != nil {
			inv.NextAttemptTime = timestamppb.New(*d.NextAttemptAt)
		}
		resp.Invoices = append(resp.Invoices, inv)
	}
	return resp, nil
}
