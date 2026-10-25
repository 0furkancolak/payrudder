// Package accounting serves the expense and accounting-summary methods of BillingService: it maps requests to commands and queries.
package accounting

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/accounting/command"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/accounting/model"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/accounting/query"
)

// Handler implements the accounting methods of billingv1.BillingServiceServer.
type Handler struct {
	app *pgxpool.Pool
}

// New returns the handler over the billing_app pool.
func New(app *pgxpool.Pool) *Handler { return &Handler{app: app} }

// CreateExpense implements billingv1.BillingServiceServer.
func (h *Handler) CreateExpense(ctx context.Context, req *billingv1.CreateExpenseRequest) (*billingv1.CreateExpenseResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	e, err := command.Create(ctx, h.app, ti.ID, &command.Input{AppID: req.GetAppId(), Vendor: req.GetVendor(), Category: req.GetCategory(), AmountMinor: req.GetAmountMinor(),
		Currency: req.GetCurrency(), IncurredOn: req.GetIncurredOn(), Note: req.GetNote(), Recurring: req.GetRecurring(), RecurringUntil: req.GetRecurringUntil()})
	if err != nil {
		return nil, err
	}
	return &billingv1.CreateExpenseResponse{Expense: expenseProto(e)}, nil
}

// UpdateExpense implements billingv1.BillingServiceServer.
func (h *Handler) UpdateExpense(ctx context.Context, req *billingv1.UpdateExpenseRequest) (*billingv1.UpdateExpenseResponse, error) {
	ti, id, err := h.scope(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	e, err := command.Update(ctx, h.app, ti.ID, id, &command.Input{AppID: req.GetAppId(), Vendor: req.GetVendor(), Category: req.GetCategory(), AmountMinor: req.GetAmountMinor(),
		Currency: req.GetCurrency(), IncurredOn: req.GetIncurredOn(), Note: req.GetNote(), Recurring: req.GetRecurring(), RecurringUntil: req.GetRecurringUntil()})
	if err != nil {
		return nil, err
	}
	return &billingv1.UpdateExpenseResponse{Expense: expenseProto(e)}, nil
}

// VoidExpense implements billingv1.BillingServiceServer.
func (h *Handler) VoidExpense(ctx context.Context, req *billingv1.VoidExpenseRequest) (*billingv1.VoidExpenseResponse, error) {
	ti, id, err := h.scope(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	e, err := command.Void(ctx, h.app, ti.ID, id, req.GetReason())
	if err != nil {
		return nil, err
	}
	return &billingv1.VoidExpenseResponse{Expense: expenseProto(e)}, nil
}

// ListExpenses implements billingv1.BillingServiceServer.
func (h *Handler) ListExpenses(ctx context.Context, req *billingv1.ListExpensesRequest) (*billingv1.ListExpensesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	list, next, err := query.List(ctx, h.app, ti.ID, query.ListParams{Category: req.GetCategory(), AppID: req.GetAppId(), Currency: req.GetCurrency(), FromDate: req.GetFromDate(),
		ToDate: req.GetToDate(), IncludeVoided: req.GetIncludeVoided(), PageSize: req.GetPageSize(), PageToken: req.GetPageToken()})
	if err != nil {
		return nil, err
	}
	out := &billingv1.ListExpensesResponse{Expenses: make([]*billingv1.Expense, 0, len(list)), NextPageToken: next}
	for i := range list {
		out.Expenses = append(out.Expenses, expenseProto(&list[i]))
	}
	return out, nil
}

// GetAccountingSummary implements billingv1.BillingServiceServer.
func (h *Handler) GetAccountingSummary(ctx context.Context, req *billingv1.GetAccountingSummaryRequest) (*billingv1.GetAccountingSummaryResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	r, err := query.Summary(ctx, h.app, ti.ID, query.SummaryParams{FromDate: req.GetFromDate(), ToDate: req.GetToDate(), AppID: req.GetAppId(), Granularity: req.GetGranularity()})
	if err != nil {
		return nil, err
	}
	out := &billingv1.GetAccountingSummaryResponse{FromDate: model.FormatDay(r.From), ToDate: model.FormatDay(r.To), Granularity: r.Granularity, AppId: r.AppID,
		Currencies: make([]*billingv1.AccountingCurrency, 0, len(r.Currencies))}
	for i := range r.Currencies {
		c := &r.Currencies[i]
		pc := &billingv1.AccountingCurrency{Currency: c.Currency, GrossIncomeMinor: c.Gross, TaxMinor: c.Tax, RefundsMinor: c.Refunds, NetIncomeMinor: c.Net, ExpensesMinor: c.Expenses,
			ProviderFeesMinor: c.ProviderFees, ProviderFeesRecorded: c.ProviderFeesRecorded, ProfitMinor: c.Profit, PaidInvoices: c.PaidInvoices,
			ExpenseByCategory: make([]*billingv1.AccountingCategoryTotal, 0, len(c.ByCategory)), Series: make([]*billingv1.AccountingPoint, 0, len(c.Series)),
			DiscountsMinor: c.Discounts, SalesMinor: c.Sales, Coupons: make([]*billingv1.AccountingCoupon, 0, len(c.Coupons))}
		for _, k := range c.Coupons {
			pc.Coupons = append(pc.Coupons, &billingv1.AccountingCoupon{CouponCode: k.CouponCode, Invoices: k.Invoices, GrossMinor: k.Gross, DiscountMinor: k.Discount, NetMinor: k.Net})
		}
		for _, k := range c.ByCategory {
			pc.ExpenseByCategory = append(pc.ExpenseByCategory, &billingv1.AccountingCategoryTotal{Category: k.Category, AmountMinor: k.Amount})
		}
		for _, p := range c.Series {
			pc.Series = append(pc.Series, &billingv1.AccountingPoint{Bucket: model.FormatDay(p.Bucket), GrossIncomeMinor: p.Gross, TaxMinor: p.Tax, RefundsMinor: p.Refunds,
				NetIncomeMinor: p.Net, ExpensesMinor: p.Expenses, ProfitMinor: p.Profit, DiscountsMinor: p.Discounts})
		}
		out.Currencies = append(out.Currencies, pc)
	}
	return out, nil
}

// GetDashboardStats implements billingv1.BillingServiceServer.
func (h *Handler) GetDashboardStats(ctx context.Context, req *billingv1.GetDashboardStatsRequest) (*billingv1.GetDashboardStatsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	cur, recent, err := query.Dashboard(ctx, h.app, ti.ID, req.GetFromDate(), req.GetToDate(), req.GetTop())
	if err != nil {
		return nil, err
	}
	out := &billingv1.GetDashboardStatsResponse{Currencies: make([]*billingv1.DashboardCurrency, 0, len(cur)), RecentInvoices: make([]*billingv1.DashboardInvoice, 0, len(recent))}
	for _, i := range recent {
		out.RecentInvoices = append(out.RecentInvoices, &billingv1.DashboardInvoice{Id: i.ID, Number: i.Number, CustomerExternalId: i.CustomerExternalID, Status: i.Status, Currency: i.Currency, TotalMinor: i.TotalMinor, CreateTime: timestamppb.New(i.CreatedAt)})
	}
	for _, c := range cur {
		pc := &billingv1.DashboardCurrency{Currency: c.Currency, TopCustomers: make([]*billingv1.DashboardTopCustomer, 0, len(c.TopCustomers)), Aging: make([]*billingv1.DashboardAgingBucket, 0, len(c.Aging))}
		for _, t := range c.TopCustomers {
			pc.TopCustomers = append(pc.TopCustomers, &billingv1.DashboardTopCustomer{CustomerId: t.CustomerID, CustomerExternalId: t.ExternalID, CustomerName: t.Name, IncomeMinor: t.Income, Invoices: t.Invoices})
		}
		for _, a := range c.Aging {
			pc.Aging = append(pc.Aging, &billingv1.DashboardAgingBucket{Bucket: a.Bucket, Count: a.Count, AmountMinor: a.Amount})
		}
		out.Currencies = append(out.Currencies, pc)
	}
	return out, nil
}

// scope returns the caller's tenant and the expense the request is about.
func (h *Handler) scope(ctx context.Context, expenseID string) (tenant.Info, uuid.UUID, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return tenant.Info{}, uuid.Nil, err
	}
	id, err := uuid.Parse(expenseID)
	if err != nil {
		return tenant.Info{}, uuid.Nil, errkey.New(codes.NotFound, "billing.accounting.expense_not_found", "no such expense")
	}
	return ti, id, nil
}

func expenseProto(e *model.Expense) *billingv1.Expense {
	out := &billingv1.Expense{Id: e.ID.String(), Vendor: e.Vendor, Category: e.Category, AmountMinor: e.AmountMinor, Currency: e.Currency, IncurredOn: model.FormatDay(e.IncurredOn),
		Note: e.Note, Recurring: e.Recurring, VoidReason: e.VoidReason, CreateTime: timestamppb.New(e.CreatedAt), UpdateTime: timestamppb.New(e.UpdatedAt)}
	if e.AppID != nil {
		out.AppId = e.AppID.String()
	}
	if e.RecurringUntil != nil {
		out.RecurringUntil = model.FormatDay(*e.RecurringUntil)
	}
	if e.VoidedAt != nil {
		out.VoidTime = timestamppb.New(*e.VoidedAt)
	}
	return out
}
