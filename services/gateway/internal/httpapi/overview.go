package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/tenant"
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

// The statuses a person sees on a payment.
const (
	payPaid       = "paid"
	payProcessing = "processing"
	payFailed     = "failed"
	payRefunded   = "refunded"
)

// ledgerEntry is a row of payments-worker's ledger. The provider reference is read only to tell one payment from another; it is not passed on.
type ledgerEntry struct {
	ID          json.Number `json:"id"`
	InvoiceID   string      `json:"invoice_id"`
	Provider    string      `json:"provider"`
	Kind        string      `json:"kind"`
	Status      string      `json:"status"`
	AmountMinor json.Number `json:"amount_minor"`
	Currency    string      `json:"currency"`
	ProviderRef string      `json:"provider_ref"`
	OccurredAt  time.Time   `json:"occurred_at"`
}

func (e ledgerEntry) amount() int64 { n, _ := e.AmountMinor.Int64(); return n }

// paymentOf is one payment (or refund) as a person sees it: the rows the ledger holds for one provider reference folded into one outcome.
type paymentOf struct {
	ledgerEntry
	shown string
}

func rank(status string) int {
	switch status {
	case "succeeded":
		return 3
	case "declined", "failed":
		return 2
	default: // pending
		return 1
	}
}

// foldLedger turns ledger rows into payments: a charge that was pending and then succeeded is one paid payment, not two. Rows without a provider
// reference stand alone. Disputes and payouts are not payments.
func foldLedger(rows []ledgerEntry) []paymentOf {
	type key struct{ provider, kind, ref string }
	best := map[key]*ledgerEntry{}
	var order []key
	for i := range rows {
		r := &rows[i]
		if r.Kind != "charge" && r.Kind != "refund" {
			continue
		}
		k := key{r.Provider, r.Kind, r.ProviderRef}
		if r.ProviderRef == "" {
			k.ref = "row:" + r.ID.String()
		}
		cur, seen := best[k]
		if !seen {
			order = append(order, k)
			best[k] = r
			continue
		}
		// The most settled outcome wins; between equals the newer row.
		if rank(r.Status) > rank(cur.Status) || (rank(r.Status) == rank(cur.Status) && r.OccurredAt.After(cur.OccurredAt)) {
			best[k] = r
		}
	}
	out := make([]paymentOf, 0, len(order))
	for _, k := range order {
		r := *best[k]
		p := paymentOf{ledgerEntry: r}
		switch {
		case r.Kind == "refund" && r.Status == "succeeded":
			p.shown = payRefunded
		case r.Kind == "refund":
			p.shown = payProcessing
			if rank(r.Status) == 2 {
				p.shown = payFailed
			}
		case r.Status == "succeeded":
			p.shown = payPaid
		case rank(r.Status) == 2:
			p.shown = payFailed
		default:
			p.shown = payProcessing
		}
		out = append(out, p)
	}
	return out
}

// aggregatePayments counts the payments of the last `days` days (in loc) by status and currency, builds a daily series of paid money per currency,
// and lists the newest payments.
func aggregatePayments(rows []ledgerEntry, now time.Time, days int, loc *time.Location, recent int) *paymentsView {
	start := startOfDay(now.In(loc), loc).AddDate(0, 0, -(days - 1))
	truncated := false
	if len(rows) >= ledgerWindowRows {
		// The newest rows were read. If even the oldest of them is inside the window, older ones may be missing.
		oldest := rows[0].OccurredAt
		for _, r := range rows {
			if r.OccurredAt.Before(oldest) {
				oldest = r.OccurredAt
			}
		}
		truncated = !oldest.Before(start)
	}
	var inWindow []paymentOf
	for _, p := range foldLedger(rows) {
		if !p.OccurredAt.Before(start) {
			inWindow = append(inWindow, p)
		}
	}
	v := &paymentsView{Truncated: truncated, ByStatus: []statusCount{}, ByCurrency: []currencyTotals{}, Series: []currencySeries{}, Recent: []paymentRow{}}
	counts := map[string]int{}
	totals := map[string]*currencyTotals{}
	daily := map[string]map[string]int64{} // currency -> date -> paid minor
	for _, p := range inWindow {
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
			daily[p.Currency][p.OccurredAt.In(loc).Format("2006-01-02")] += p.amount()
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
	for _, st := range []string{payPaid, payProcessing, payFailed, payRefunded} {
		v.ByStatus = append(v.ByStatus, statusCount{Status: st, Count: counts[st]})
	}
	for _, t := range totals {
		v.ByCurrency = append(v.ByCurrency, *t)
	}
	sort.Slice(v.ByCurrency, func(i, j int) bool { return v.ByCurrency[i].Currency < v.ByCurrency[j].Currency })
	for _, t := range v.ByCurrency {
		s := currencySeries{Currency: t.Currency}
		for d := 0; d < days; d++ {
			date := start.AddDate(0, 0, d).Format("2006-01-02")
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

func (s *Server) appOverview(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	envName := r.URL.Query().Get("environment")
	if envName == "" {
		envName = "live"
	}
	if envName != "live" && envName != "sandbox" {
		writeError(w, r, invalid("environment", "sandbox or live"))
		return
	}
	app, err := s.d.Identity.GetMyApp(r.Context(), &plansv1.GetMyAppRequest{UserId: sess.UserID, AppId: r.PathValue("id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	var t *plansv1.Tenant
	for _, c := range app.GetApp().GetTenants() {
		if (envName == "live") == (c.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE) {
			t = c
		}
	}
	if t == nil {
		writeError(w, r, errkey.New(codes.NotFound, "identity.tenant.not_found", "this app has no such environment"))
		return
	}
	p := principalOf(t)
	ctx, cancel := context.WithTimeout(tenant.NewContext(r.Context(), tenant.Info{ID: p.Tenant.ID, Environment: p.Tenant.Environment, Timezone: p.Tenant.Timezone, Tier: p.Tier}), overviewTimeout)
	defer cancel()
	o := s.buildOverview(ctx, app.GetApp().GetId(), envName, t)
	noStoreJSON(w)
	writeJSONValue(w, http.StatusOK, o)
}

func noStoreJSON(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }

// buildOverview reads the sections in parallel. ctx already carries the tenant.
func (s *Server) buildOverview(ctx context.Context, appID, envName string, t *plansv1.Tenant) *overviewResponse {
	o := &overviewResponse{AppID: appID, Environment: envName, TenantID: t.GetId(), Currency: t.GetCurrency(), WindowDays: overviewWindowDays,
		GeneratedAt: time.Now().UTC(), Unavailable: []string{}, Subscriptions: []subscriptionV{}}
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		loc = time.UTC
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		down []string
	)
	section := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				mu.Lock()
				down = append(down, name)
				mu.Unlock()
			}
		}()
	}
	var rows []ledgerEntry
	var providers []providerView
	paymentsUp := true
	section("payments", func() error {
		entries, err := s.recentLedger(ctx, t.GetId())
		if err != nil {
			paymentsUp = false
			return status.Error(codes.Unavailable, "payments")
		}
		rows = entries
		var accts struct {
			Accounts []providerView `json:"accounts"`
		}
		if code, err := s.paymentsDo(ctx, http.MethodGet, "/admin/tenants/"+url.PathEscape(t.GetId())+"/provider-accounts", nil, nil, &accts); err == nil && code/100 == 2 {
			providers = accts.Accounts
		}
		return nil
	})
	inv := &invoicesView{Revenue: []revenueTotals{}, Recent: []invoiceBrief{}}
	section("invoices", func() error {
		count := func(st string) (countView, error) {
			resp, err := s.d.Billing.ListInvoices(ctx, &billingv1.ListInvoicesRequest{Status: st, PageSize: listCountCap})
			return countView{Count: len(resp.GetInvoices()), More: resp.GetNextPageToken() != ""}, err
		}
		var err error
		if inv.Open, err = count("open"); err != nil {
			return err
		}
		if inv.Paid, err = count("paid"); err != nil {
			return err
		}
		recent, err := s.d.Billing.ListInvoices(ctx, &billingv1.ListInvoicesRequest{PageSize: 6})
		if err != nil {
			return err
		}
		for _, i := range recent.GetInvoices() {
			inv.Recent = append(inv.Recent, invoiceBrief{ID: i.GetId(), Number: i.GetNumber(), CustomerID: i.GetCustomerId(), CustomerRef: i.GetCustomerExternalId(),
				Status: i.GetStatus(), Currency: i.GetCurrency(), TotalMinor: i.GetTotalMinor(), CreateTime: i.GetCreateTime().AsTime(), FinalizedTime: i.GetFinalizeTime().AsTime()})
		}
		rev, err := s.d.Billing.GetRevenueReport(ctx, &billingv1.GetRevenueReportRequest{})
		if err != nil {
			return err
		}
		byCur := map[string]*revenueTotals{}
		for _, r := range rev.GetRows() {
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
		for _, c := range byCur {
			inv.Revenue = append(inv.Revenue, *c)
		}
		sort.Slice(inv.Revenue, func(i, j int) bool { return inv.Revenue[i].Currency < inv.Revenue[j].Currency })
		return nil
	})
	var customers countView
	section("customers", func() error {
		resp, err := s.d.Plans.ListCustomers(ctx, &plansv1.ListCustomersRequest{PageSize: 500})
		customers = countView{Count: len(resp.GetCustomers()), More: resp.GetNextPageToken() != ""}
		return err
	})
	var subs []subscriptionV
	section("subscriptions", func() error {
		resp, err := s.d.Plans.GetSubscriptionReport(ctx, &plansv1.GetSubscriptionReportRequest{})
		for _, r := range resp.GetRows() {
			subs = append(subs, subscriptionV{Currency: r.GetCurrency(), Active: r.GetActive(), Trialing: r.GetTrialing(), PastDue: r.GetPastDue(),
				MRRMinor: r.GetMrrMinor(), NewMRR: r.GetNewMrrMinor(), ChurnedMR: r.GetChurnedMrrMinor()})
		}
		return err
	})
	var endpoints, failed int
	var failedMore bool
	section("webhooks", func() error {
		eps, err := s.d.Webhooks.ListWebhookEndpoints(ctx, &plansv1.ListWebhookEndpointsRequest{})
		if err != nil {
			return err
		}
		endpoints = len(eps.GetEndpoints())
		ds, err := s.d.Webhooks.ListWebhookDeliveries(ctx, &plansv1.ListWebhookDeliveriesRequest{State: plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED, PageSize: listCountCap})
		failed, failedMore = len(ds.GetDeliveries()), ds.GetNextPageToken() != ""
		return err
	})
	wg.Wait()

	slices.Sort(down)
	o.Unavailable = append(o.Unavailable, down...)
	has := func(name string) bool { return !slices.Contains(down, name) }
	if has("payments") {
		o.Payments = aggregatePayments(rows, time.Now(), overviewWindowDays, loc, 8)
	}
	if has("invoices") {
		o.Invoices = inv
	}
	if has("customers") {
		o.Customers = &customers
	}
	if has("subscriptions") && subs != nil {
		o.Subscriptions = subs
	}
	o.Health = healthView{PaymentsReachable: paymentsUp, Providers: []providerView{}}
	if providers != nil {
		o.Health.Providers = providers
	}
	if has("webhooks") {
		o.Health.WebhookEndpoints, o.Health.WebhookFailed, o.Health.WebhookFailedMore = endpoints, failed, failedMore
	}
	o.Status = overviewStatus(o)
	return o
}
