package query

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// TopCustomer is what one customer's invoices that were paid in the period came to, in one currency.
type TopCustomer struct {
	CustomerID, ExternalID, Name string
	Income, Invoices             int64
}

// AgingBucket is the open invoices of one currency that have been open for a range of days.
type AgingBucket struct {
	Bucket        string
	Count, Amount int64
}

// RecentInvoice is an invoice of the activity feed.
type RecentInvoice struct {
	ID, Number, CustomerExternalID, Status, Currency string
	TotalMinor                                       int64
	CreatedAt                                        time.Time
}

// DashboardCurrency is the dashboard facts of one currency.
type DashboardCurrency struct {
	Currency     string
	TopCustomers []TopCustomer
	Aging        []AgingBucket
}

// agingOrder is the order the ageing buckets are shown in (the database sorts them as text, where "61+" would come before "8-30").
var agingOrder = []string{"0-7", "8-30", "31-60", "61+"}

// Dashboard returns, per currency, the top customers of the period and the age of the open invoices. The period is the same one the summary takes (UTC days,
// at most five years); top is how many customers per currency, 1 to 10 (default 5).
func Dashboard(ctx context.Context, app *pgxpool.Pool, tenantID uuid.UUID, fromDate, toDate string, top int32) ([]DashboardCurrency, []RecentInvoice, error) {
	from, to, _, err := period(SummaryParams{FromDate: fromDate, ToDate: toDate, Granularity: GranMonth})
	if err != nil {
		return nil, nil, err
	}
	if top < 1 {
		top = 5
	}
	top = min(top, 10)
	byCur := map[string]*DashboardCurrency{}
	get := func(c string) *DashboardCurrency {
		if byCur[c] == nil {
			byCur[c] = &DashboardCurrency{Currency: c}
		}
		return byCur[c]
	}
	var recent []RecentInvoice
	err = pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		ri, err := q.DashboardRecentInvoices(ctx, tenantID)
		if err != nil {
			return err
		}
		for _, r := range ri {
			recent = append(recent, RecentInvoice{ID: r.ID.String(), Number: r.Number, CustomerExternalID: r.CustomerExternalID, Status: r.Status, Currency: r.Currency, TotalMinor: r.TotalMinor, CreatedAt: r.CreatedAt})
		}
		tc, err := q.DashboardTopCustomers(ctx, sqlc.DashboardTopCustomersParams{TenantID: tenantID, FromAt: from, ToAt: to.AddDate(0, 0, 1), TopN: top})
		if err != nil {
			return err
		}
		for _, r := range tc {
			c := get(r.Currency)
			c.TopCustomers = append(c.TopCustomers, TopCustomer{CustomerID: r.CustomerID.String(), ExternalID: r.CustomerExternalID, Name: r.CustomerName, Income: r.IncomeMinor, Invoices: r.Invoices})
		}
		ag, err := q.DashboardOpenAging(ctx, tenantID)
		if err != nil {
			return err
		}
		for _, r := range ag {
			c := get(r.Currency)
			c.Aging = append(c.Aging, AgingBucket{Bucket: r.Bucket, Count: r.Invoices, Amount: r.AmountMinor})
		}
		return nil
	})
	if err != nil {
		return nil, nil, internal()
	}
	out := make([]DashboardCurrency, 0, len(byCur))
	for _, c := range byCur {
		slices.SortFunc(c.Aging, func(a, b AgingBucket) int {
			return slices.Index(agingOrder, a.Bucket) - slices.Index(agingOrder, b.Bucket)
		})
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b DashboardCurrency) int {
		switch {
		case a.Currency < b.Currency:
			return -1
		case a.Currency > b.Currency:
			return 1
		}
		return 0
	})
	return out, recent, nil
}
