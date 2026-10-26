package httpapi

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"time"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// The overview of one environment of an app: what a person wants to see first about "my application's payments". It is composed here from the services
// that own the facts (payments-worker's ledger, billing's invoices and revenue report, plans' customers, subscriptions and webhooks) with the
// environment's tenant as the caller, so every number is the tenant's own. A section whose service does not answer is left out and named in
// `unavailable`; the rest of the page still works. Money is never added across currencies: every amount sits under its currency.

const (
	overviewWindowDays = 30
	overviewTimeout    = 6 * time.Second
	ledgerWindowRows   = 1000
	listCountCap       = 100
)

type overviewResponse struct {
	AppID         string          `json:"app_id"`
	Environment   string          `json:"environment"`
	TenantID      string          `json:"tenant_id"`
	Currency      string          `json:"currency"`
	WindowDays    int             `json:"window_days"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Status        string          `json:"status"`
	Unavailable   []string        `json:"unavailable"`
	Payments      *paymentsView   `json:"payments"`
	Invoices      *invoicesView   `json:"invoices"`
	Customers     *countView      `json:"customers"`
	Subscriptions []subscriptionV `json:"subscriptions"`
	Health        healthView      `json:"health"`
}

type countView struct {
	Count int `json:"count"`
	// More is set when the count stopped at the cap: there are at least this many.
	More bool `json:"more"`
}

type statusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type currencyTotals struct {
	Currency      string `json:"currency"`
	PaidCount     int    `json:"paid_count"`
	PaidMinor     int64  `json:"paid_minor"`
	FailedCount   int    `json:"failed_count"`
	FailedMinor   int64  `json:"failed_minor"`
	PendingCount  int    `json:"pending_count"`
	PendingMinor  int64  `json:"pending_minor"`
	RefundedMinor int64  `json:"refunded_minor"`
}

type seriesPoint struct {
	Date        string `json:"date"`
	AmountMinor int64  `json:"amount_minor"`
}

type currencySeries struct {
	Currency string        `json:"currency"`
	Points   []seriesPoint `json:"points"`
}

type paymentRow struct {
	ID          string    `json:"id"`
	InvoiceID   string    `json:"invoice_id"`
	Provider    string    `json:"provider"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type paymentsView struct {
	// Truncated is set when the ledger window held more rows than the gateway reads (the newest are counted).
	Truncated  bool             `json:"truncated"`
	ByStatus   []statusCount    `json:"by_status"`
	ByCurrency []currencyTotals `json:"by_currency"`
	Series     []currencySeries `json:"series"`
	Recent     []paymentRow     `json:"recent"`
}

type revenueTotals struct {
	Currency         string `json:"currency"`
	BilledMinor      int64  `json:"billed_minor"`
	CollectedMinor   int64  `json:"collected_minor"`
	OutstandingMinor int64  `json:"outstanding_minor"`
	RefundedMinor    int64  `json:"refunded_minor"`
}

type invoiceBrief struct {
	ID            string    `json:"id"`
	Number        string    `json:"number"`
	CustomerID    string    `json:"customer_id"`
	CustomerRef   string    `json:"customer_external_id"`
	Status        string    `json:"status"`
	Currency      string    `json:"currency"`
	TotalMinor    int64     `json:"total_minor"`
	CreateTime    time.Time `json:"create_time"`
	FinalizedTime time.Time `json:"finalize_time"`
}

type invoicesView struct {
	Open    countView       `json:"open"`
	Paid    countView       `json:"paid"`
	Revenue []revenueTotals `json:"revenue"`
	Recent  []invoiceBrief  `json:"recent"`
}

type subscriptionV struct {
	Currency  string `json:"currency"`
	Active    int64  `json:"active"`
	Trialing  int64  `json:"trialing"`
	PastDue   int64  `json:"past_due"`
	MRRMinor  int64  `json:"mrr_minor"`
	NewMRR    int64  `json:"new_mrr_minor"`
	ChurnedMR int64  `json:"churned_mrr_minor"`
}

type providerView struct {
	Provider    string `json:"provider"`
	Environment string `json:"environment"`
	IsDefault   bool   `json:"is_default"`
}

type healthView struct {
	// PaymentsReachable is false when payments-worker did not answer.
	PaymentsReachable bool `json:"payments_reachable"`
	WebhookEndpoints  int  `json:"webhook_endpoints"`
	WebhookFailed     int  `json:"webhook_failed"`
	// WebhookFailedMore is set when there are more failed deliveries than the count shows.
	WebhookFailedMore bool           `json:"webhook_failed_more"`
	Providers         []providerView `json:"providers"`
}

// aggregatePayments counts the payments of the last `days` days (in loc) by status and currency, builds a daily series of paid money per currency,
// and lists the newest payments.
func aggregatePayments(rows []ledgerEntry, now time.Time, days int, loc *time.Location, recent int) *paymentsView {
	start := startOfDay(now.In(loc), loc).AddDate(0, 0, -(days - 1))
	var inWindow []paymentOf
	for _, p := range foldLedger(rows) {
		if !p.OccurredAt.Before(start) {
			inWindow = append(inWindow, p)
		}
	}
	v := &paymentsView{Truncated: truncatedSince(rows, start), ByStatus: []statusCount{}, ByCurrency: []currencyTotals{}, Series: []currencySeries{}, Recent: []paymentRow{}}
	counts, totals, daily := countPayments(inWindow, loc)
	for _, st := range []string{payPaid, payProcessing, payFailed, payRefunded} {
		v.ByStatus = append(v.ByStatus, statusCount{Status: st, Count: counts[st]})
	}
	for _, t := range totals {
		v.ByCurrency = append(v.ByCurrency, *t)
	}
	sort.Slice(v.ByCurrency, func(i, j int) bool { return v.ByCurrency[i].Currency < v.ByCurrency[j].Currency })
	for _, t := range v.ByCurrency {
		s := currencySeries{Currency: t.Currency}
		for d := range days {
			date := start.AddDate(0, 0, d).Format(time.DateOnly)
			s.Points = append(s.Points, seriesPoint{Date: date, AmountMinor: daily[t.Currency][date]})
		}
		v.Series = append(v.Series, s)
	}
	slices.SortStableFunc(inWindow, func(a, b paymentOf) int { return b.OccurredAt.Compare(a.OccurredAt) })
	for i, p := range inWindow {
		if i == recent {
			break
		}
		v.Recent = append(v.Recent, paymentRow{ID: p.ID.String(), InvoiceID: p.InvoiceID, Provider: p.Provider, Kind: p.Kind, Status: p.shown,
			AmountMinor: p.amount(), Currency: p.Currency, OccurredAt: p.OccurredAt})
	}
	return v
}

// truncatedSince says the ledger window may be missing older rows that belong to the period starting at start: it was read to its limit, and even the oldest
// row it holds is inside the period.
func truncatedSince(rows []ledgerEntry, start time.Time) bool {
	if len(rows) < ledgerWindowRows {
		return false
	}
	oldest := rows[0].OccurredAt
	for _, r := range rows {
		if r.OccurredAt.Before(oldest) {
			oldest = r.OccurredAt
		}
	}
	return !oldest.Before(start)
}

// countPayments counts payments by the status shown and by currency, and sums the paid money per currency and day (in loc).
func countPayments(payments []paymentOf, loc *time.Location) (counts map[string]int, totals map[string]*currencyTotals, daily map[string]map[string]int64) {
	counts, totals, daily = map[string]int{}, map[string]*currencyTotals{}, map[string]map[string]int64{}
	for _, p := range payments {
		counts[p.shown]++
		t := totals[p.Currency]
		if t == nil {
			t = &currencyTotals{Currency: p.Currency}
			totals[p.Currency] = t
		}
		switch p.shown {
		case payPaid:
			t.PaidCount++
			t.PaidMinor += p.amount()
			if daily[p.Currency] == nil {
				daily[p.Currency] = map[string]int64{}
			}
			daily[p.Currency][p.OccurredAt.In(loc).Format(time.DateOnly)] += p.amount()
		case payFailed:
			t.FailedCount++
			t.FailedMinor += p.amount()
		case payProcessing:
			t.PendingCount++
			t.PendingMinor += p.amount()
		case payRefunded:
			t.RefundedMinor += p.amount()
		}
	}
	return counts, totals, daily
}

func startOfDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// overviewStatus is the one word the page leads with: degraded when a section did not answer, attention when something needs a person (a failed
// payment or webhook delivery), otherwise ok.
func overviewStatus(o *overviewResponse) string {
	if len(o.Unavailable) > 0 {
		return "degraded"
	}
	if o.Health.WebhookFailed > 0 {
		return "attention"
	}
	if o.Payments != nil {
		for _, s := range o.Payments.ByStatus {
			if s.Status == payFailed && s.Count > 0 {
				return "attention"
			}
		}
	}
	return "ok"
}

// appOverview is GET /app/v1/apps/{id}/overview?environment=.
func (s *Server) appOverview(w http.ResponseWriter, r *http.Request) {
	appID, envName, t, ok := s.appTenant(w, r)
	if !ok {
		return
	}
	ctx, cancel := tenantContext(r.Context(), t, overviewTimeout)
	defer cancel()
	o := s.buildOverview(ctx, appID, envName, t)
	noStoreJSON(w)
	writeJSONValue(w, http.StatusOK, o)
}

func noStoreJSON(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }

// overviewSources is what the services answered for one overview. Each section fills only its own fields.
type overviewSources struct {
	ledger            []ledgerEntry  // payments
	providers         []providerView // payments
	paymentsUp        bool           // payments-worker answered
	invoices          *invoicesView
	customers         countView
	subscriptions     []subscriptionV
	endpoints, failed int // webhooks
	failedMore        bool
}

// overviewFetcher reads the sections of one overview from the services that own the facts.
type overviewFetcher struct {
	s   *Server
	ctx context.Context
	t   *plansv1.Tenant
	src overviewSources
}

// buildOverview reads the sections in parallel, then puts what they answered together. ctx already carries the tenant.
func (s *Server) buildOverview(ctx context.Context, appID, envName string, t *plansv1.Tenant) *overviewResponse {
	f := &overviewFetcher{s: s, ctx: ctx, t: t, src: overviewSources{paymentsUp: true, invoices: &invoicesView{Revenue: []revenueTotals{}, Recent: []invoiceBrief{}}}}
	var run sectionRunner
	run.run("payments", f.payments)
	run.run("invoices", f.invoices)
	run.run("customers", f.customers)
	run.run("subscriptions", f.subscriptions)
	run.run("webhooks", f.webhooks)
	return assembleOverview(appID, envName, t, &f.src, run.wait(), time.Now())
}

// assembleOverview is the pure part of the overview: it sorts what the sections answered into the page. A section in down is left out and named in
// `unavailable`.
func assembleOverview(appID, envName string, t *plansv1.Tenant, src *overviewSources, down sectionsDown, now time.Time) *overviewResponse {
	o := &overviewResponse{AppID: appID, Environment: envName, TenantID: t.GetId(), Currency: t.GetCurrency(), WindowDays: overviewWindowDays,
		GeneratedAt: now.UTC(), Unavailable: []string{}, Subscriptions: []subscriptionV{}}
	o.Unavailable = append(o.Unavailable, down...)
	if down.has("payments") {
		o.Payments = aggregatePayments(src.ledger, now, overviewWindowDays, tenantLocation(t.GetTimezone()), overviewRecentPayments)
	}
	if down.has("invoices") {
		o.Invoices = src.invoices
	}
	if down.has("customers") {
		o.Customers = &src.customers
	}
	if down.has("subscriptions") && src.subscriptions != nil {
		o.Subscriptions = src.subscriptions
	}
	o.Health = newHealth(src.paymentsUp, src.providers)
	if down.has("webhooks") {
		o.Health.WebhookEndpoints, o.Health.WebhookFailed, o.Health.WebhookFailedMore = src.endpoints, src.failed, src.failedMore
	}
	o.Status = overviewStatus(o)
	return o
}

// payments is the newest ledger rows and the provider accounts, from payments-worker.
func (f *overviewFetcher) payments() error {
	rows, providers, err := f.s.paymentsHealth(f.ctx, f.t.GetId())
	f.src.paymentsUp = err == nil
	f.src.ledger, f.src.providers = rows, providers
	return err
}

// invoices is billing's open and paid counts, the newest invoices and the revenue report.
func (f *overviewFetcher) invoices() error {
	inv := f.src.invoices
	count := func(st string) (countView, error) {
		resp, err := f.s.d.Billing.ListInvoices(f.ctx, &billingv1.ListInvoicesRequest{Status: st, PageSize: listCountCap})
		return countView{Count: len(resp.GetInvoices()), More: resp.GetNextPageToken() != ""}, err
	}
	var err error
	if inv.Open, err = count("open"); err != nil {
		return err
	}
	if inv.Paid, err = count("paid"); err != nil {
		return err
	}
	recent, err := f.s.d.Billing.ListInvoices(f.ctx, &billingv1.ListInvoicesRequest{PageSize: overviewRecentInvoices})
	if err != nil {
		return err
	}
	for _, i := range recent.GetInvoices() {
		inv.Recent = append(inv.Recent, invoiceBrief{ID: i.GetId(), Number: i.GetNumber(), CustomerID: i.GetCustomerId(), CustomerRef: i.GetCustomerExternalId(),
			Status: i.GetStatus(), Currency: i.GetCurrency(), TotalMinor: i.GetTotalMinor(), CreateTime: i.GetCreateTime().AsTime(), FinalizedTime: i.GetFinalizeTime().AsTime()})
	}
	rev, err := f.s.d.Billing.GetRevenueReport(f.ctx, &billingv1.GetRevenueReportRequest{})
	if err != nil {
		return err
	}
	inv.Revenue = revenueByCurrency(rev.GetRows())
	return nil
}

// revenueByCurrency adds the revenue report's rows up per currency (never across currencies), in currency order.
func revenueByCurrency(rows []*billingv1.RevenueRow) []revenueTotals {
	byCur := map[string]*revenueTotals{}
	for _, r := range rows {
		c := byCur[r.GetCurrency()]
		if c == nil {
			c = &revenueTotals{Currency: r.GetCurrency()}
			byCur[r.GetCurrency()] = c
		}
		c.BilledMinor += r.GetBilledMinor()
		c.CollectedMinor += r.GetCollectedMinor()
		c.OutstandingMinor += r.GetOutstandingMinor()
		c.RefundedMinor += r.GetRefundedMinor()
	}
	out := []revenueTotals{}
	for _, c := range byCur {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}

// customers counts the customers up to overviewCustomerCountLimit.
func (f *overviewFetcher) customers() error {
	resp, err := f.s.d.Plans.ListCustomers(f.ctx, &plansv1.ListCustomersRequest{PageSize: overviewCustomerCountLimit})
	f.src.customers = countView{Count: len(resp.GetCustomers()), More: resp.GetNextPageToken() != ""}
	return err
}

// subscriptions is plans' subscription report, one line per currency.
func (f *overviewFetcher) subscriptions() error {
	resp, err := f.s.d.Plans.GetSubscriptionReport(f.ctx, &plansv1.GetSubscriptionReportRequest{})
	for _, r := range resp.GetRows() {
		f.src.subscriptions = append(f.src.subscriptions, subscriptionV{Currency: r.GetCurrency(), Active: r.GetActive(), Trialing: r.GetTrialing(), PastDue: r.GetPastDue(),
			MRRMinor: r.GetMrrMinor(), NewMRR: r.GetNewMrrMinor(), ChurnedMR: r.GetChurnedMrrMinor()})
	}
	return err
}

// webhooks counts the endpoints and the failed deliveries.
func (f *overviewFetcher) webhooks() error {
	var err error
	f.src.endpoints, f.src.failed, f.src.failedMore, err = f.s.webhookHealth(f.ctx)
	return err
}
