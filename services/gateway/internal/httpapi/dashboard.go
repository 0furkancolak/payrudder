package httpapi

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

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
	dashUsageSample  = 10
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

// ---- the period

type dashPeriod struct {
	Preset           string
	From, To         time.Time // the first and the last day, midnights in the tenant's zone
	Days             int
	Granularity      string
	Compare          bool
	PrevFrom, PrevTo time.Time
}

func (p dashPeriod) rangeJSON() dashRange {
	r := dashRange{Period: p.Preset, From: dayStr(p.From), To: dayStr(p.To), Days: p.Days, Granularity: p.Granularity, Compare: p.Compare}
	if p.Compare {
		r.PrevFrom, r.PrevTo = dayStr(p.PrevFrom), dayStr(p.PrevTo)
	}
	return r
}

func dayStr(t time.Time) string { return t.Format("2006-01-02") }

// parseDashPeriod reads ?period=7d|30d|90d|ytd|custom (default 30d), ?from=&to= for custom, and ?compare=0 to switch the comparison off. Days are the tenant's.
func parseDashPeriod(q url.Values, now time.Time, loc *time.Location) (dashPeriod, error) {
	today := startOfDay(now.In(loc), loc)
	p := dashPeriod{Preset: q.Get("period"), To: today, Compare: q.Get("compare") != "0"}
	if p.Preset == "" {
		p.Preset = "30d"
	}
	switch p.Preset {
	case "7d", "30d", "90d":
		n, _ := strconv.Atoi(p.Preset[:len(p.Preset)-1])
		p.From = today.AddDate(0, 0, -(n - 1))
	case "ytd":
		p.From = time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, loc)
	case "custom":
		from, err1 := time.ParseInLocation("2006-01-02", q.Get("from"), loc)
		to, err2 := time.ParseInLocation("2006-01-02", q.Get("to"), loc)
		if err1 != nil || err2 != nil {
			return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.bad_period", "a custom period needs from and to as days like 2026-10-21")
		}
		if to.After(today) {
			to = today
		}
		if from.After(to) {
			return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.bad_period", "from must not be after to")
		}
		p.From, p.To = from, to
	default:
		return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.bad_period", "period is 7d, 30d, 90d, ytd or custom")
	}
	p.Days = int(math.Round(p.To.Sub(p.From).Hours()/24)) + 1
	if p.Days > dashMaxDays {
		return p, errkey.New(codes.InvalidArgument, "gateway.dashboard.range_too_long", "a period is at most two years")
	}
	p.Granularity = "day"
	if p.Days > dashDayThreshold {
		p.Granularity = "month"
	}
	p.PrevTo = p.From.AddDate(0, 0, -1)
	p.PrevFrom = p.PrevTo.AddDate(0, 0, -(p.Days - 1))
	return p, nil
}

// dashBuckets lists the buckets of a series: every day, or the first day of every month the period touches.
func dashBuckets(from, to time.Time, gran string) []string {
	var out []string
	if gran == "month" {
		for d := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location()); !d.After(to); d = d.AddDate(0, 1, 0) {
			out = append(out, dayStr(d))
		}
		return out
	}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, dayStr(d))
	}
	return out
}

// ---- payments

// dashPayments folds the ledger rows inside [from, to+1day) into one block per currency.
func paymentsByCurrency(rows []paymentOf, from, to time.Time) map[string]*dashPayments {
	end := to.AddDate(0, 0, 1)
	out := map[string]*dashPayments{}
	for _, p := range rows {
		if p.OccurredAt.Before(from) || !p.OccurredAt.Before(end) {
			continue
		}
		b := out[p.Currency]
		if b == nil {
			b = &dashPayments{}
			out[p.Currency] = b
		}
		switch p.shown {
		case payPaid:
			b.Paid++
			b.PaidMinor += p.amount()
		case payFailed:
			b.Failed++
			b.FailedMinor += p.amount()
		case payProcessing:
			b.Processing++
		case payRefunded:
			b.Refunds++
			b.RefundedMinor += p.amount()
		}
	}
	for _, b := range out {
		if settled := b.Paid + b.Failed; settled > 0 {
			r := int(math.Round(float64(b.Failed) * 10000 / float64(settled)))
			b.FailedRateBP = &r
		}
	}
	return out
}

// ---- subscriptions

func subsOf(rows []*plansv1.SubscriptionReportRow, month string) map[string]*dashSubs {
	out := map[string]*dashSubs{}
	for _, r := range rows {
		b := out[r.GetCurrency()]
		if b == nil {
			b = &dashSubs{Month: month}
			out[r.GetCurrency()] = b
		}
		b.Active += r.GetActive()
		b.Trialing += r.GetTrialing()
		b.Paused += r.GetPaused()
		b.PastDue += r.GetPastDue()
		b.MRRMinor += r.GetMrrMinor()
		b.New += r.GetNewSubscriptions()
		b.NewMRRMinor += r.GetNewMrrMinor()
		b.Churned += r.GetChurned()
		b.ChurnedMRR += r.GetChurnedMrrMinor()
	}
	for _, b := range out {
		b.ARRMinor = b.MRRMinor * 12
		alive := b.Active + b.Trialing + b.Paused + b.PastDue
		if start := alive - b.New + b.Churned; start > 0 {
			r := int(math.Round(float64(b.Churned) * 10000 / float64(start)))
			b.ChurnRateBP = &r
		}
	}
	return out
}

// arpu is net revenue per active subscriber in a currency; null when there is none.
func arpu(net int64, subs *dashSubs) *int64 {
	if subs == nil || subs.Active+subs.PastDue < 1 {
		return nil
	}
	v := int64(math.Round(float64(net) / float64(subs.Active+subs.PastDue)))
	return &v
}

// nextCharge is the next moment a subscription is charged. A fee in advance is charged on the anniversary of the start (of the trial's end when there was a
// trial), a month or a year apart; everything else (a fee in arrears, usage) is invoiced when the month closes, which is the first of the next month. It reads
// the subscription as the API shows it: the exact moment can differ by the billing run's hour, so the page shows days.
func nextCharge(s *plansv1.Subscription, now time.Time, loc *time.Location) time.Time {
	if s.GetFeeTiming() == plansv1.FeeTiming_FEE_TIMING_ADVANCE && s.GetOwner() != "provider" {
		base := s.GetStartTime().AsTime().In(loc)
		if s.GetTrialEndTime() != nil {
			base = s.GetTrialEndTime().AsTime().In(loc)
		}
		step := func(n int) time.Time {
			if s.GetInterval() == plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR {
				return base.AddDate(n, 0, 0)
			}
			return base.AddDate(0, n, 0)
		}
		for n := 0; n < 5000; n++ {
			if t := step(n); t.After(now) {
				return t
			}
		}
	}
	y, m, _ := now.In(loc).Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
}

func renewalsOf(subs []*plansv1.Subscription, now time.Time, loc *time.Location, horizonDays, perCurrency int) map[string][]dashRenewal {
	horizon := now.AddDate(0, 0, horizonDays)
	out := map[string][]dashRenewal{}
	for _, s := range subs {
		st := s.GetState()
		if s.GetEndTime() != nil || s.GetCancelAtPeriodEnd() || (st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE && st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING && st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE) {
			continue
		}
		at := nextCharge(s, now, loc)
		if at.After(horizon) {
			continue
		}
		out[s.GetCurrency()] = append(out[s.GetCurrency()], dashRenewal{SubscriptionID: s.GetId(), CustomerRef: s.GetCustomerExternalId(), PlanCode: s.GetPlanCode(),
			Interval: map[plansv1.SubscriptionInterval]string{plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR: "year"}[s.GetInterval()], RenewsAt: at.UTC(), State: stateWord(st)})
	}
	for c := range out {
		slices.SortFunc(out[c], func(a, b dashRenewal) int { return a.RenewsAt.Compare(b.RenewsAt) })
		if len(out[c]) > perCurrency {
			out[c] = out[c][:perCurrency]
		}
	}
	return out
}

func stateWord(s plansv1.SubscriptionState) string {
	switch s {
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING:
		return "trialing"
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE:
		return "past_due"
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED:
		return "paused"
	case plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED:
		return "cancelled"
	default:
		return "active"
	}
}

// ---- usage against limits

// usageOf groups the limit statuses of some customers by meter: how many are over their limit, how many are past 80%, and the five closest to theirs.
func usageOf(byCustomer map[string][]*limitsv1.LimitStatus) []usageMeter {
	type acc struct {
		m    usageMeter
		rows []usageCustomer
	}
	meters := map[string]*acc{}
	refs := make([]string, 0, len(byCustomer))
	for ref := range byCustomer {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	for _, ref := range refs {
		for _, st := range byCustomer[ref] {
			if st.GetUnlimited() {
				continue
			}
			a := meters[st.GetMeterCode()]
			if a == nil {
				a = &acc{m: usageMeter{MeterCode: st.GetMeterCode()}}
				meters[st.GetMeterCode()] = a
			}
			used, _ := strconv.ParseFloat(st.GetUsed(), 64)
			limit, _ := strconv.ParseFloat(st.GetLimit(), 64)
			pct := 0.0
			if limit > 0 {
				pct = math.Round(used/limit*1000) / 10 // display only: the exact decimals are the entitlements endpoint's
			} else if used > 0 {
				pct = 100
			}
			a.m.Customers++
			if !st.GetAllowed() {
				a.m.Over++
			} else if pct >= 80 {
				a.m.Near++
			}
			a.rows = append(a.rows, usageCustomer{CustomerRef: ref, Used: st.GetUsed(), Limit: st.GetLimit(), Percent: pct})
		}
	}
	out := make([]usageMeter, 0, len(meters))
	for _, a := range meters {
		slices.SortStableFunc(a.rows, func(x, y usageCustomer) int { return cmp.Compare(y.Percent, x.Percent) })
		a.m.Top = a.rows[:min(5, len(a.rows))]
		out = append(out, a.m)
	}
	slices.SortFunc(out, func(a, b usageMeter) int {
		if c := cmp.Compare(b.Over, a.Over); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Near, a.Near); c != 0 {
			return c
		}
		return cmp.Compare(a.MeterCode, b.MeterCode)
	})
	return out
}

// ---- the endpoints

type dashCache struct {
	mu sync.Mutex
	m  map[string]dashCacheEntry
}

type dashCacheEntry struct {
	at   time.Time
	body []byte
}

func (c *dashCache) get(key string, now time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) > dashCacheTTL {
		return nil, false
	}
	return e.body, true
}

func (c *dashCache) set(key string, now time.Time, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]dashCacheEntry{}
	}
	if len(c.m) >= 128 {
		for k, e := range c.m {
			if now.Sub(e.at) > dashCacheTTL {
				delete(c.m, k)
			}
		}
		for k := range c.m {
			if len(c.m) < 128 {
				break
			}
			delete(c.m, k)
		}
	}
	c.m[key] = dashCacheEntry{at: now, body: body}
}

// appDashboard is GET /app/v1/apps/{id}/dashboard?environment=&period=&from=&to=&compare=.
func (s *Server) appDashboard(w http.ResponseWriter, r *http.Request) {
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
	s.serveDashboard(w, r, app.GetApp().GetId(), envName, t)
}

// adminTenantDashboard is GET /admin/v1/tenants/{tenant_id}/dashboard: the same dashboard for the operator, for any tenant.
func (s *Server) adminTenantDashboard(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Admin.GetTenant(r.Context(), &plansv1.GetTenantRequest{TenantId: r.PathValue("tenant_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	t := resp.GetTenant()
	env := "sandbox"
	if t.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE {
		env = "live"
	}
	s.serveDashboard(w, r, t.GetAppId(), env, t)
}

func (s *Server) serveDashboard(w http.ResponseWriter, r *http.Request, appID, envName string, t *plansv1.Tenant) {
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		loc = time.UTC
	}
	now := time.Now()
	per, err := parseDashPeriod(r.URL.Query(), now, loc)
	if err != nil {
		writeError(w, r, err)
		return
	}
	noStoreJSON(w)
	key := t.GetId() + "|" + appID + "|" + per.Preset + "|" + dayStr(per.From) + "|" + dayStr(per.To) + "|" + strconv.FormatBool(per.Compare)
	if body, ok := s.dash.get(key, now); ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}
	p := principalOf(t)
	ctx, cancel := context.WithTimeout(tenant.NewContext(r.Context(), tenant.Info{ID: p.Tenant.ID, Environment: p.Tenant.Environment, Timezone: p.Tenant.Timezone, Tier: p.Tier}), dashTimeout)
	defer cancel()
	out := s.buildDashboard(ctx, appID, envName, t, per, loc, now)
	body, err := json.Marshal(out)
	if err != nil {
		writeError(w, r, status.Error(codes.Internal, "internal error"))
		return
	}
	// A page that lost a section is not remembered: the next look should try again.
	if len(out.Unavailable) == 0 {
		s.dash.set(key, now, body)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// buildDashboard reads the sections in parallel. ctx already carries the tenant.
func (s *Server) buildDashboard(ctx context.Context, appID, envName string, t *plansv1.Tenant, per dashPeriod, loc *time.Location, now time.Time) *dashboardResponse {
	o := &dashboardResponse{AppID: appID, Environment: envName, TenantID: t.GetId(), Timezone: t.GetTimezone(), DefaultCurrency: t.GetCurrency(), Range: per.rangeJSON(),
		GeneratedAt: now.UTC(), Unavailable: []string{}, Currencies: []*dashCurrency{}, Activity: []activityItem{}}
	var (
		mu   sync.Mutex
		down []string
	)
	var g errgroup.Group
	section := func(name string, fn func() error) {
		g.Go(func() error {
			if err := fn(); err != nil {
				mu.Lock()
				down = append(down, name)
				mu.Unlock()
			}
			return nil
		})
	}
	from, to := dayStr(per.From), dayStr(per.To)

	var cur, prev *billingv1.GetAccountingSummaryResponse
	section("revenue", func() error {
		var err error
		if cur, err = s.d.Billing.GetAccountingSummary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: from, ToDate: to, Granularity: per.Granularity}); err != nil {
			return err
		}
		if per.Compare {
			prev, err = s.d.Billing.GetAccountingSummary(ctx, &billingv1.GetAccountingSummaryRequest{FromDate: dayStr(per.PrevFrom), ToDate: dayStr(per.PrevTo), Granularity: per.Granularity})
		}
		return err
	})
	var stats *billingv1.GetDashboardStatsResponse
	section("invoices", func() error {
		var err error
		stats, err = s.d.Billing.GetDashboardStats(ctx, &billingv1.GetDashboardStatsRequest{FromDate: from, ToDate: to, Top: 5})
		return err
	})
	curMonth := time.Date(per.To.Year(), per.To.Month(), 1, 0, 0, 0, 0, loc)
	prevMonth := curMonth.AddDate(0, -1, 0)
	var subsNow, subsPrev *plansv1.GetSubscriptionReportResponse
	section("subscriptions", func() error {
		var err error
		if subsNow, err = s.d.Plans.GetSubscriptionReport(ctx, &plansv1.GetSubscriptionReportRequest{Month: curMonth.Format("2006-01"), GroupBy: []string{"plan"}}); err != nil {
			return err
		}
		if per.Compare {
			subsPrev, err = s.d.Plans.GetSubscriptionReport(ctx, &plansv1.GetSubscriptionReportRequest{Month: prevMonth.Format("2006-01"), GroupBy: []string{"plan"}})
		}
		return err
	})
	var cust *plansv1.GetCustomerStatsResponse
	section("customers", func() error {
		req := &plansv1.GetCustomerStatsRequest{FromTime: tsOf(per.From), ToTime: tsOf(per.To.AddDate(0, 0, 1))}
		if per.Compare {
			req.PrevFromTime, req.PrevToTime = tsOf(per.PrevFrom), tsOf(per.PrevTo.AddDate(0, 0, 1))
		}
		var err error
		cust, err = s.d.Plans.GetCustomerStats(ctx, req)
		return err
	})
	var ledger []ledgerEntry
	var providers []providerView
	paymentsUp := true
	section("payments", func() error {
		rows, err := s.recentLedger(ctx, t.GetId())
		if err != nil {
			paymentsUp = false
			return errkey.New(codes.Unavailable, "gateway.dashboard.payments_unavailable", "payments")
		}
		ledger = rows
		var accts struct {
			Accounts []providerView `json:"accounts"`
		}
		if code, err := s.paymentsDo(ctx, http.MethodGet, "/admin/tenants/"+url.PathEscape(t.GetId())+"/provider-accounts", nil, nil, &accts); err == nil && code/100 == 2 {
			providers = accts.Accounts
		}
		return nil
	})
	var subList *plansv1.ListSubscriptionsResponse
	section("renewals", func() error {
		var err error
		subList, err = s.d.Plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{PageSize: 200})
		return err
	})
	usageByCustomer := map[string][]*limitsv1.LimitStatus{}
	var usageSampled int
	var usagePeriod string
	section("usage", func() error {
		if s.d.Limits == nil {
			return errkey.New(codes.Unavailable, "gateway.dashboard.limits_unavailable", "limits")
		}
		// Which customers to look at comes from the subscriptions; the limits are read for at most ten of them.
		list, err := s.d.Plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{PageSize: 50})
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		type cu struct{ id, ref string }
		var picks []cu
		for _, sub := range list.GetSubscriptions() {
			st := sub.GetState()
			if sub.GetEndTime() != nil || seen[sub.GetCustomerId()] || (st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE && st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING && st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE) {
				continue
			}
			seen[sub.GetCustomerId()] = true
			if picks = append(picks, cu{sub.GetCustomerId(), sub.GetCustomerExternalId()}); len(picks) == dashUsageSample {
				break
			}
		}
		var um sync.Mutex
		var eg errgroup.Group
		eg.SetLimit(5)
		failed := 0
		for _, c := range picks {
			eg.Go(func() error {
				resp, err := s.d.Limits.GetLimitStatus(ctx, &limitsv1.GetLimitStatusRequest{CustomerId: c.id})
				um.Lock()
				defer um.Unlock()
				if err != nil {
					failed++
					return nil
				}
				usageByCustomer[c.ref] = resp.GetStatuses()
				if len(resp.GetStatuses()) > 0 && usagePeriod == "" {
					usagePeriod = resp.GetStatuses()[0].GetPeriodId()
				}
				return nil
			})
		}
		_ = eg.Wait()
		usageSampled = len(picks)
		if len(picks) > 0 && failed == len(picks) {
			return errkey.New(codes.Unavailable, "gateway.dashboard.limits_unavailable", "limits")
		}
		return nil
	})
	var endpoints, failedHooks int
	var failedMore bool
	section("webhooks", func() error {
		eps, err := s.d.Webhooks.ListWebhookEndpoints(ctx, &plansv1.ListWebhookEndpointsRequest{})
		if err != nil {
			return err
		}
		endpoints = len(eps.GetEndpoints())
		ds, err := s.d.Webhooks.ListWebhookDeliveries(ctx, &plansv1.ListWebhookDeliveriesRequest{State: plansv1.WebhookDeliveryState_WEBHOOK_DELIVERY_STATE_FAILED, PageSize: listCountCap})
		failedHooks, failedMore = len(ds.GetDeliveries()), ds.GetNextPageToken() != ""
		return err
	})
	var recentSubs *plansv1.ListSubscriptionsResponse
	section("activity", func() error {
		var err error
		recentSubs, err = s.d.Plans.ListSubscriptions(ctx, &plansv1.ListSubscriptionsRequest{PageSize: 6})
		return err
	})
	_ = g.Wait()

	slices.Sort(down)
	o.Unavailable = append(o.Unavailable, down...)
	has := func(name string) bool { return !slices.Contains(down, name) }

	blocks := map[string]*dashCurrency{}
	block := func(c string) *dashCurrency {
		if blocks[c] == nil {
			blocks[c] = &dashCurrency{Currency: c, Series: []dashPoint{}, TopCustomers: []dashTopCustomer{}, TopPackages: []dashPackage{}, Aging: []dashAging{}, Renewals: []dashRenewal{}}
		}
		return blocks[c]
	}
	block(t.GetCurrency())
	buckets := dashBuckets(per.From, per.To, per.Granularity)
	prevBuckets := dashBuckets(per.PrevFrom, per.PrevTo, per.Granularity)

	// The revenue series of each currency, by bucket, and the one before it; a currency with no accounting block gets zeros below.
	netOf := map[string]map[string]int64{}
	prevNetOf := map[string]map[string]int64{}
	if has("revenue") {
		for _, c := range cur.GetCurrencies() {
			block(c.GetCurrency()).Revenue = revenueOf(c)
			netOf[c.GetCurrency()] = map[string]int64{}
			for _, p := range c.GetSeries() {
				netOf[c.GetCurrency()][p.GetBucket()] = p.GetNetIncomeMinor()
			}
		}
		for _, c := range prev.GetCurrencies() {
			rv := revenueOf(c)
			block(c.GetCurrency()).PrevRevenue = &rv
			prevNetOf[c.GetCurrency()] = map[string]int64{}
			for _, p := range c.GetSeries() {
				prevNetOf[c.GetCurrency()][p.GetBucket()] = p.GetNetIncomeMinor()
			}
		}
	}
	if has("invoices") {
		for _, c := range stats.GetCurrencies() {
			b := block(c.GetCurrency())
			for _, tc := range c.GetTopCustomers() {
				b.TopCustomers = append(b.TopCustomers, dashTopCustomer{CustomerID: tc.GetCustomerId(), ExternalID: tc.GetCustomerExternalId(), Name: tc.GetCustomerName(), IncomeMinor: tc.GetIncomeMinor(), Invoices: tc.GetInvoices()})
			}
			for _, a := range c.GetAging() {
				b.Aging = append(b.Aging, dashAging{Bucket: a.GetBucket(), Count: a.GetCount(), AmountMinor: a.GetAmountMinor()})
				b.OpenCount += a.GetCount()
				b.OpenMinor += a.GetAmountMinor()
			}
		}
	}
	if has("subscriptions") {
		now := subsOf(subsNow.GetRows(), curMonth.Format("2006-01"))
		for c, v := range now {
			block(c).Subscriptions = v
		}
		if per.Compare {
			for c, v := range subsOf(subsPrev.GetRows(), prevMonth.Format("2006-01")) {
				block(c).PrevSubscriptions = v
			}
		}
		byPlan := map[string][]dashPackage{}
		for _, r := range subsNow.GetRows() {
			if r.GetActive()+r.GetTrialing()+r.GetPastDue() > 0 {
				byPlan[r.GetCurrency()] = append(byPlan[r.GetCurrency()], dashPackage{PlanCode: r.GetPlanCode(), Active: r.GetActive() + r.GetTrialing() + r.GetPastDue(), MRRMinor: r.GetMrrMinor()})
			}
		}
		for c, list := range byPlan {
			slices.SortStableFunc(list, func(a, b dashPackage) int {
				if x := cmp.Compare(b.MRRMinor, a.MRRMinor); x != 0 {
					return x
				}
				return cmp.Compare(b.Active, a.Active)
			})
			block(c).TopPackages = list[:min(5, len(list))]
		}
	}
	if has("customers") {
		o.Customers = &dashCustomers{Total: cust.GetTotal(), New: cust.GetNewInRange(), PrevNew: cust.GetNewInPrevious()}
	}
	if has("payments") {
		folded := foldLedger(ledger)
		start, oldest := per.From, now
		for _, r := range ledger {
			if r.OccurredAt.Before(oldest) {
				oldest = r.OccurredAt
			}
		}
		full := len(ledger) >= ledgerWindowRows
		o.PaymentsTruncated = full && !oldest.Before(start)
		o.PaymentsPrevCovered = !full || oldest.Before(per.PrevFrom)
		for c, v := range paymentsByCurrency(folded, per.From, per.To) {
			block(c).Payments = v
		}
		if per.Compare && o.PaymentsPrevCovered {
			for c, v := range paymentsByCurrency(folded, per.PrevFrom, per.PrevTo) {
				block(c).PrevPayments = v
			}
		}
		o.Activity = append(o.Activity, paymentActivity(folded, 8)...)
	}
	if has("renewals") {
		o.RenewalsTruncated = subList.GetNextPageToken() != ""
		for c, list := range renewalsOf(subList.GetSubscriptions(), now, loc, 30, 6) {
			block(c).Renewals = list
		}
	}
	if has("usage") {
		o.Usage = &dashUsage{PeriodID: usagePeriod, Sampled: usageSampled, Meters: usageOf(usageByCustomer)}
		if o.Usage.Meters == nil {
			o.Usage.Meters = []usageMeter{}
		}
	}
	if has("invoices") {
		for _, i := range stats.GetRecentInvoices() {
			amt := i.GetTotalMinor()
			label := i.GetNumber()
			if label == "" {
				label = "draft"
			}
			o.Activity = append(o.Activity, activityItem{Kind: "invoice", At: i.GetCreateTime().AsTime(), Ref: i.GetId(), Label: label, Customer: i.GetCustomerExternalId(), Status: i.GetStatus(), AmountMinor: &amt, Currency: i.GetCurrency()})
		}
	}
	if has("activity") {
		for _, sub := range recentSubs.GetSubscriptions() {
			o.Activity = append(o.Activity, activityItem{Kind: "subscription", At: sub.GetCreateTime().AsTime(), Ref: sub.GetId(), Label: sub.GetPlanCode(), Customer: sub.GetCustomerExternalId(), Status: stateWord(sub.GetState()), Currency: sub.GetCurrency()})
		}
	}
	if has("revenue") {
		for code, b := range blocks {
			if per.Compare && b.PrevRevenue == nil {
				b.PrevRevenue = &dashRevenue{}
			}
			b.Series = make([]dashPoint, 0, len(buckets))
			for i, d := range buckets {
				pt := dashPoint{Date: d, NetMinor: netOf[code][d]}
				if per.Compare && i < len(prevBuckets) {
					v := prevNetOf[code][prevBuckets[i]]
					pt.PrevDate, pt.PrevNetMinor = prevBuckets[i], &v
				}
				b.Series = append(b.Series, pt)
			}
		}
	}
	slices.SortStableFunc(o.Activity, func(a, b activityItem) int { return b.At.Compare(a.At) })
	if len(o.Activity) > 12 {
		o.Activity = o.Activity[:12]
	}

	// The blocks: the tenant's own currency first, then by net revenue, biggest first; a currency is never ranked against another's money by value alone,
	// only the order of the tabs is decided here, and it is the order a person would want (their own currency, then where the money is).
	for _, b := range blocks {
		if b.Subscriptions != nil || b.PrevSubscriptions != nil {
			if b.Subscriptions != nil {
				b.ArpuMinor = arpu(b.Revenue.NetMinor, b.Subscriptions)
			}
			if b.PrevRevenue != nil && b.PrevSubscriptions != nil {
				b.PrevArpuMinor = arpu(b.PrevRevenue.NetMinor, b.PrevSubscriptions)
			}
		}
		o.Currencies = append(o.Currencies, b)
	}
	sort.SliceStable(o.Currencies, func(i, j int) bool {
		a, b := o.Currencies[i], o.Currencies[j]
		if (a.Currency == t.GetCurrency()) != (b.Currency == t.GetCurrency()) {
			return a.Currency == t.GetCurrency()
		}
		if a.Revenue.NetMinor != b.Revenue.NetMinor {
			return a.Revenue.NetMinor > b.Revenue.NetMinor
		}
		return a.Currency < b.Currency
	})

	o.Health = healthView{PaymentsReachable: paymentsUp, Providers: []providerView{}}
	if providers != nil {
		o.Health.Providers = providers
	}
	if has("webhooks") {
		o.Health.WebhookEndpoints, o.Health.WebhookFailed, o.Health.WebhookFailedMore = endpoints, failedHooks, failedMore
	}
	o.Status = dashboardStatus(o)
	return o
}

func revenueOf(c *billingv1.AccountingCurrency) dashRevenue {
	return dashRevenue{GrossMinor: c.GetGrossIncomeMinor(), TaxMinor: c.GetTaxMinor(), RefundsMinor: c.GetRefundsMinor(), NetMinor: c.GetNetIncomeMinor(), ExpensesMinor: c.GetExpensesMinor(),
		ProfitMinor: c.GetProfitMinor(), PaidInvoices: c.GetPaidInvoices()}
}

func paymentActivity(folded []paymentOf, n int) []activityItem {
	list := slices.Clone(folded)
	slices.SortStableFunc(list, func(a, b paymentOf) int { return b.OccurredAt.Compare(a.OccurredAt) })
	var out []activityItem
	for i, p := range list {
		if i == n {
			break
		}
		amt := p.amount()
		out = append(out, activityItem{Kind: "payment", At: p.OccurredAt, Ref: p.ID.String(), Label: p.Kind, Status: p.shown, AmountMinor: &amt, Currency: p.Currency, Customer: ""})
	}
	return out
}

// dashboardStatus is the one word the page leads with, as in the overview.
func dashboardStatus(o *dashboardResponse) string {
	if len(o.Unavailable) > 0 {
		return "degraded"
	}
	if o.Health.WebhookFailed > 0 {
		return "attention"
	}
	for _, c := range o.Currencies {
		if c.Payments != nil && c.Payments.Failed > 0 {
			return "attention"
		}
		for _, a := range c.Aging {
			if a.Bucket == "61+" && a.Count > 0 {
				return "attention"
			}
		}
	}
	return "ok"
}

func tsOf(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }
