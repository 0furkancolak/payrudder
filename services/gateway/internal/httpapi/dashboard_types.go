package httpapi

import "time"

// The dashboard of one environment of an app (GET /app/v1/apps/{id}/dashboard, and the operator's twin under /admin/v1/tenants/{id}/dashboard): the
// overview's facts over a period the person chooses, with the period before it to compare with, one block per currency. Like the overview it is composed here
// from the services that own the facts, with the environment's tenant as the caller, and a section whose service does not answer is left out and named in
// `unavailable`. Money is never added, converted or compared across currencies: every amount sits in the block of its currency.
//
// Where each number comes from, because a dashboard that disagrees with the books is worse than none:
//   - revenue, refunds, tax, expenses and the daily series are billing's accounting summary: the same "an invoice counts on the day its saga reached PAID"
//     rule, so the dashboard and the accounting page show the same net income;
//   - payments are the payment ledger (payments-worker), folded to one outcome per provider reference; attempts, not invoices: a declined card that dunning
//     retries is several failed attempts. Refunds are counted apart from charges. The ledger window is the newest 1000 rows, and `payments_truncated` says when
//     the period reaches past it;
//   - subscriptions, MRR, new and churned are plans' subscription report for the month the period ends in (and the month before it, to compare);
//   - new customers are plans' customer counts over the period and the one before it.

const (
	dashTimeout      = 8 * time.Second
	dashMaxDays      = 731
	dashDayThreshold = 92 // up to this many days the revenue series is daily, beyond it monthly
	dashCacheTTL     = 15 * time.Second
	dashCacheEntries = 128 // dashboards remembered at once
	dashUsageSample  = 10  // customers whose limits are read
	// monthLayout is a month as plans-svc and the periods name it: 2026-10.
	monthLayout = "2006-01"

	dashTop                    = 5   // top customers asked of billing, and top packages and top customers of a meter shown
	dashTopPackages            = 5   // packages shown per currency
	dashUsageTop               = 5   // customers closest to their limit shown per meter
	dashUsageNearPercent       = 80  // a customer at or past this share of a limit is "near"
	dashUsageCandidates        = 50  // subscriptions looked through to pick the customers whose limits are read
	dashUsageParallel          = 5   // limit lookups in flight at once
	dashRenewalSubscriptions   = 200 // subscriptions the upcoming charges are worked out from
	dashRenewalHorizonDays     = 30  // how far ahead renewals are listed
	dashRenewalsPerCurrency    = 6
	dashActivitySubscriptions  = 6  // newest subscriptions read for the activity feed
	dashActivityPayments       = 8  // newest payments in the activity feed
	dashActivityMax            = 12 // items in the activity feed
	overviewRecentPayments     = 8
	overviewRecentInvoices     = 6
	overviewCustomerCountLimit = 500
)

type dashRange struct {
	Period      string `json:"period"`
	From        string `json:"from"`
	To          string `json:"to"`
	Days        int    `json:"days"`
	Granularity string `json:"granularity"`
	Compare     bool   `json:"compare"`
	PrevFrom    string `json:"prev_from,omitempty"`
	PrevTo      string `json:"prev_to,omitempty"`
}

type dashRevenue struct {
	GrossMinor    int64 `json:"gross_minor"`
	TaxMinor      int64 `json:"tax_minor"`
	RefundsMinor  int64 `json:"refunds_minor"`
	NetMinor      int64 `json:"net_minor"`
	ExpensesMinor int64 `json:"expenses_minor"`
	ProfitMinor   int64 `json:"profit_minor"`
	PaidInvoices  int64 `json:"paid_invoices"`
}

type dashPoint struct {
	Date         string `json:"date"`
	NetMinor     int64  `json:"net_minor"`
	PrevDate     string `json:"prev_date,omitempty"`
	PrevNetMinor *int64 `json:"prev_net_minor,omitempty"`
}

type dashPayments struct {
	// Attempts at charging: paid, failed (a declined card retried is several), and still processing.
	Paid          int   `json:"paid"`
	Failed        int   `json:"failed"`
	Processing    int   `json:"processing"`
	Refunds       int   `json:"refunds"`
	PaidMinor     int64 `json:"paid_minor"`
	FailedMinor   int64 `json:"failed_minor"`
	RefundedMinor int64 `json:"refunded_minor"`
	// FailedRateBP is failed / (paid + failed) in basis points; null when there was no settled attempt.
	FailedRateBP *int `json:"failed_rate_bp"`
}

type dashSubs struct {
	Month       string `json:"month"`
	Active      int64  `json:"active"`
	Trialing    int64  `json:"trialing"`
	Paused      int64  `json:"paused"`
	PastDue     int64  `json:"past_due"`
	MRRMinor    int64  `json:"mrr_minor"`
	ARRMinor    int64  `json:"arr_minor"`
	New         int64  `json:"new"`
	NewMRRMinor int64  `json:"new_mrr_minor"`
	Churned     int64  `json:"churned"`
	ChurnedMRR  int64  `json:"churned_mrr_minor"`
	// ChurnRateBP is churned / (those at the month's start) in basis points; null when nobody was there.
	ChurnRateBP *int `json:"churn_rate_bp"`
}

type dashTopCustomer struct {
	CustomerID  string `json:"customer_id"`
	ExternalID  string `json:"customer_external_id"`
	Name        string `json:"name"`
	IncomeMinor int64  `json:"income_minor"`
	Invoices    int64  `json:"invoices"`
}

type dashPackage struct {
	PlanCode string `json:"plan_code"`
	Active   int64  `json:"active"`
	MRRMinor int64  `json:"mrr_minor"`
}

type dashAging struct {
	Bucket      string `json:"bucket"`
	Count       int64  `json:"count"`
	AmountMinor int64  `json:"amount_minor"`
}

type dashRenewal struct {
	SubscriptionID string    `json:"subscription_id"`
	CustomerRef    string    `json:"customer_external_id"`
	PlanCode       string    `json:"plan_code"`
	Interval       string    `json:"interval"`
	RenewsAt       time.Time `json:"renews_at"`
	State          string    `json:"state"`
}

type dashCurrency struct {
	Currency          string            `json:"currency"`
	Revenue           dashRevenue       `json:"revenue"`
	PrevRevenue       *dashRevenue      `json:"prev_revenue"`
	Series            []dashPoint       `json:"series"`
	Payments          *dashPayments     `json:"payments"`
	PrevPayments      *dashPayments     `json:"prev_payments"`
	Subscriptions     *dashSubs         `json:"subscriptions"`
	PrevSubscriptions *dashSubs         `json:"prev_subscriptions"`
	ArpuMinor         *int64            `json:"arpu_minor"`
	PrevArpuMinor     *int64            `json:"prev_arpu_minor"`
	TopCustomers      []dashTopCustomer `json:"top_customers"`
	TopPackages       []dashPackage     `json:"top_packages"`
	Aging             []dashAging       `json:"aging"`
	OpenCount         int64             `json:"open_count"`
	OpenMinor         int64             `json:"open_minor"`
	Renewals          []dashRenewal     `json:"renewals"`
}

type dashCustomers struct {
	Total   int64 `json:"total"`
	New     int64 `json:"new"`
	PrevNew int64 `json:"prev_new"`
}

type usageCustomer struct {
	CustomerRef string  `json:"customer_external_id"`
	Used        string  `json:"used"`
	Limit       string  `json:"limit"`
	Percent     float64 `json:"percent"`
}

type usageMeter struct {
	MeterCode string          `json:"meter_code"`
	Customers int             `json:"customers"`
	Over      int             `json:"over"`
	Near      int             `json:"near"`
	Top       []usageCustomer `json:"top"`
}

type dashUsage struct {
	PeriodID string       `json:"period_id"`
	Sampled  int          `json:"sampled"`
	Meters   []usageMeter `json:"meters"`
}

type activityItem struct {
	Kind        string    `json:"kind"` // invoice, payment, subscription
	At          time.Time `json:"at"`
	Ref         string    `json:"ref"`
	Label       string    `json:"label"`
	Customer    string    `json:"customer,omitempty"`
	Status      string    `json:"status"`
	AmountMinor *int64    `json:"amount_minor,omitempty"`
	Currency    string    `json:"currency,omitempty"`
}

type dashboardResponse struct {
	AppID               string          `json:"app_id"`
	Environment         string          `json:"environment"`
	TenantID            string          `json:"tenant_id"`
	Timezone            string          `json:"timezone"`
	DefaultCurrency     string          `json:"default_currency"`
	Range               dashRange       `json:"range"`
	GeneratedAt         time.Time       `json:"generated_at"`
	Status              string          `json:"status"`
	Unavailable         []string        `json:"unavailable"`
	PaymentsTruncated   bool            `json:"payments_truncated"`
	PaymentsPrevCovered bool            `json:"payments_prev_covered"`
	RenewalsTruncated   bool            `json:"renewals_truncated"`
	Currencies          []*dashCurrency `json:"currencies"`
	Customers           *dashCustomers  `json:"customers"`
	Usage               *dashUsage      `json:"usage"`
	Activity            []activityItem  `json:"activity"`
	Health              healthView      `json:"health"`
}
