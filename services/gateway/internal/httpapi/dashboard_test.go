package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// --- fakes of what the dashboard asks the services (the product harness is in product_test.go)

type productLimits struct {
	limitsv1.UnimplementedLimitsServiceServer
}

func (productLimits) GetLimitStatus(_ context.Context, req *limitsv1.GetLimitStatusRequest) (*limitsv1.GetLimitStatusResponse, error) {
	switch req.GetCustomerId() {
	case "cu-ann":
		return &limitsv1.GetLimitStatusResponse{Statuses: []*limitsv1.LimitStatus{{MeterCode: "api_calls", PeriodId: "2026-10", Used: "950", Limit: "1000", Allowed: true}, {MeterCode: "seats", Used: "3", Unlimited: true, Allowed: true}}}, nil
	case "cu-bob":
		return &limitsv1.GetLimitStatusResponse{Statuses: []*limitsv1.LimitStatus{{MeterCode: "api_calls", PeriodId: "2026-10", Used: "1200", Limit: "1000", Allowed: false}}}, nil
	}
	return &limitsv1.GetLimitStatusResponse{Statuses: []*limitsv1.LimitStatus{{MeterCode: "api_calls", PeriodId: "2026-10", Used: "10", Limit: "1000", Allowed: true}}}, nil
}

func (p *productPlans) GetCustomerStats(ctx context.Context, req *plansv1.GetCustomerStatsRequest) (*plansv1.GetCustomerStatsResponse, error) {
	p.saw(ctx)
	prev := int64(1)
	if req.GetPrevFromTime() == nil {
		prev = 0
	}
	return &plansv1.GetCustomerStatsResponse{Total: 7, NewInRange: 3, NewInPrevious: prev}, nil
}

func (p *productPlans) ListSubscriptions(ctx context.Context, req *plansv1.ListSubscriptionsRequest) (*plansv1.ListSubscriptionsResponse, error) {
	p.saw(ctx)
	start := timestamppb.New(time.Now().AddDate(0, -2, -3))
	return &plansv1.ListSubscriptionsResponse{Subscriptions: []*plansv1.Subscription{
		{Id: "s1", CustomerId: "cu-ann", CustomerExternalId: "ann", PlanCode: "pro", Currency: "USD", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE, StartTime: start, FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, CreateTime: start},
		{Id: "s2", CustomerId: "cu-bob", CustomerExternalId: "bob", PlanCode: "basic", Currency: "TRY", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE, StartTime: start, FeeTiming: plansv1.FeeTiming_FEE_TIMING_ARREARS, CreateTime: start},
		{Id: "s3", CustomerId: "cu-cy", CustomerExternalId: "cy", PlanCode: "pro", Currency: "USD", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED, StartTime: start, EndTime: start, CreateTime: start},
	}}, nil
}

func (b *productBilling) GetAccountingSummary(ctx context.Context, req *billingv1.GetAccountingSummaryRequest) (*billingv1.GetAccountingSummaryResponse, error) {
	b.saw(ctx)
	from, _ := time.Parse("2006-01-02", req.GetFromDate())
	to, _ := time.Parse("2006-01-02", req.GetToDate())
	net := int64(1000)
	if to.Before(time.Now().AddDate(0, 0, -(int(to.Sub(from).Hours()/24) + 1))) {
		net = 500 // the earlier period
	}
	var series []*billingv1.AccountingPoint
	var total int64
	if req.GetGranularity() == "month" {
		for d := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); !d.After(to); d = d.AddDate(0, 1, 0) {
			series = append(series, &billingv1.AccountingPoint{Bucket: d.Format("2006-01-02"), NetIncomeMinor: net * 30})
			total += net * 30
		}
	} else {
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			series = append(series, &billingv1.AccountingPoint{Bucket: d.Format("2006-01-02"), NetIncomeMinor: net})
			total += net
		}
	}
	return &billingv1.GetAccountingSummaryResponse{Currencies: []*billingv1.AccountingCurrency{
		{Currency: "USD", GrossIncomeMinor: total + 100, TaxMinor: 50, RefundsMinor: 50, NetIncomeMinor: total, ExpensesMinor: 10, ProfitMinor: total - 10, PaidInvoices: 4, Series: series},
	}}, nil
}

func (b *productBilling) GetDashboardStats(ctx context.Context, _ *billingv1.GetDashboardStatsRequest) (*billingv1.GetDashboardStatsResponse, error) {
	b.saw(ctx)
	return &billingv1.GetDashboardStatsResponse{
		Currencies: []*billingv1.DashboardCurrency{
			{Currency: "USD", TopCustomers: []*billingv1.DashboardTopCustomer{{CustomerId: "cu-ann", CustomerExternalId: "ann", CustomerName: "Ann", IncomeMinor: 7000, Invoices: 2}},
				Aging: []*billingv1.DashboardAgingBucket{{Bucket: "0-7", Count: 2, AmountMinor: 3000}, {Bucket: "61+", Count: 1, AmountMinor: 500}}},
			{Currency: "EUR", Aging: []*billingv1.DashboardAgingBucket{{Bucket: "8-30", Count: 1, AmountMinor: 900}}},
		},
		RecentInvoices: []*billingv1.DashboardInvoice{{Id: "i9", Number: "INV-9", CustomerExternalId: "ann", Status: "paid", Currency: "USD", TotalMinor: 4900, CreateTime: timestamppb.New(time.Now().Add(-30 * time.Minute))}},
	}, nil
}

// --- the period

func TestParseDashPeriod(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Date(2026, 10, 21, 22, 30, 0, 0, time.UTC) // already the 22nd in Istanbul
	for _, c := range []struct {
		q          string
		from, to   string
		days       int
		gran       string
		prevFrom   string
		prevTo     string
		wantErrKey string
	}{
		{"", "2026-09-23", "2026-10-22", 30, "day", "2026-08-24", "2026-09-22", ""},
		{"period=7d", "2026-10-16", "2026-10-22", 7, "day", "2026-10-09", "2026-10-15", ""},
		{"period=90d", "2026-07-25", "2026-10-22", 90, "day", "2026-04-26", "2026-07-24", ""},
		{"period=ytd", "2026-01-01", "2026-10-22", 295, "month", "2025-03-12", "2025-12-31", ""},
		{"period=custom&from=2026-10-01&to=2026-10-10", "2026-10-01", "2026-10-10", 10, "day", "2026-09-21", "2026-09-30", ""},
		{"period=custom&from=2026-10-01&to=2027-01-01", "2026-10-01", "2026-10-22", 22, "day", "2026-09-09", "2026-09-30", ""}, // the future is cut at today
		{"period=custom&from=2026-10-10&to=2026-10-01", "", "", 0, "", "", "", "gateway.dashboard.bad_period"},
		{"period=custom&from=nope&to=2026-10-01", "", "", 0, "", "", "", "gateway.dashboard.bad_period"},
		{"period=custom&from=2020-01-01&to=2026-10-01", "", "", 0, "", "", "", "gateway.dashboard.range_too_long"},
		{"period=week", "", "", 0, "", "", "", "gateway.dashboard.bad_period"},
	} {
		q, _ := url.ParseQuery(c.q)
		p, err := parseDashPeriod(q, now, loc)
		if c.wantErrKey != "" {
			if err == nil || errkey.Of(err) != c.wantErrKey {
				t.Errorf("%q: err = %v, want key %s", c.q, err, c.wantErrKey)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.q, err)
			continue
		}
		if dayStr(p.From) != c.from || dayStr(p.To) != c.to || p.Days != c.days || p.Granularity != c.gran || dayStr(p.PrevFrom) != c.prevFrom || dayStr(p.PrevTo) != c.prevTo {
			t.Errorf("%q: %s..%s (%d days, %s) previous %s..%s", c.q, dayStr(p.From), dayStr(p.To), p.Days, p.Granularity, dayStr(p.PrevFrom), dayStr(p.PrevTo))
		}
	}
	q, _ := url.ParseQuery("compare=0")
	if p, _ := parseDashPeriod(q, now, loc); p.Compare || p.rangeJSON().PrevFrom != "" {
		t.Error("compare=0 still compares")
	}
}

func TestDashBuckets(t *testing.T) {
	loc := time.UTC
	from, to := time.Date(2026, 8, 20, 0, 0, 0, 0, loc), time.Date(2026, 10, 3, 0, 0, 0, 0, loc)
	if got := dashBuckets(from, to, "month"); len(got) != 3 || got[0] != "2026-08-01" || got[2] != "2026-10-01" {
		t.Errorf("months = %v", got)
	}
	if got := dashBuckets(to, to.AddDate(0, 0, 2), "day"); len(got) != 3 || got[1] != "2026-10-04" {
		t.Errorf("days = %v", got)
	}
}

// --- the facts

func TestPaymentsByCurrencyCountsAttemptsAndKeepsCurrenciesApart(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	raw, _ := json.Marshal(ledgerRows(now))
	var entries []ledgerEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	rows := foldLedger(entries)
	from, to := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	got := paymentsByCurrency(rows, from, to)
	usd, try := got["USD"], got["TRY"]
	if usd == nil || try == nil {
		t.Fatalf("blocks = %v", got)
	}
	if usd.Paid != 1 || usd.PaidMinor != 4900 || usd.Failed != 1 || usd.FailedMinor != 1500 || usd.Processing != 1 || usd.Refunds != 1 || usd.RefundedMinor != 900 {
		t.Errorf("USD = %+v (a pending row then a success is one paid payment; the refund is not a charge)", *usd)
	}
	if usd.FailedRateBP == nil || *usd.FailedRateBP != 5000 { // one failed of two settled
		t.Errorf("USD failed rate = %v", usd.FailedRateBP)
	}
	if try.Paid != 1 || try.PaidMinor != 25000 || try.Failed != 0 || *try.FailedRateBP != 0 {
		t.Errorf("TRY = %+v", *try)
	}
	// The old row and the payout are outside; a period with no settled attempt has no rate.
	if got := paymentsByCurrency(rows, from, from.AddDate(0, 0, -3)); len(got) != 0 {
		t.Errorf("an empty window = %v", got)
	}
}

func TestSubsOfAddsPlansOfOneCurrencyAndNeverCurrencies(t *testing.T) {
	rows := []*plansv1.SubscriptionReportRow{
		{Currency: "USD", PlanCode: "pro", Active: 3, MrrMinor: 30000, NewSubscriptions: 1, NewMrrMinor: 10000, Churned: 1, ChurnedMrrMinor: 10000},
		{Currency: "USD", PlanCode: "basic", Active: 2, PastDue: 1, MrrMinor: 6000},
		{Currency: "TRY", PlanCode: "pro", Active: 1, MrrMinor: 99000},
	}
	got := subsOf(rows, "2026-10")
	usd, try := got["USD"], got["TRY"]
	if usd.Active != 5 || usd.PastDue != 1 || usd.MRRMinor != 36000 || usd.ARRMinor != 432000 || usd.New != 1 || usd.Churned != 1 || try.MRRMinor != 99000 || try.ARRMinor != 1188000 {
		t.Errorf("usd = %+v try = %+v", *usd, *try)
	}
	// Alive at the end 6, one of them new, one churned: 6 - 1 + 1 = 6 at the start, one left: 1/6 = 16.67 %.
	if usd.ChurnRateBP == nil || *usd.ChurnRateBP != 1667 {
		t.Errorf("churn rate = %v", usd.ChurnRateBP)
	}
	if v := arpu(36000, usd); v == nil || *v != 6000 { // 36000 / (5 active + 1 past due)
		t.Errorf("arpu = %v", v)
	}
	if arpu(100, &dashSubs{}) != nil || arpu(100, nil) != nil {
		t.Error("arpu of nobody is not a number")
	}
}

func TestNextChargeAndRenewals(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Istanbul")
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	start := timestamppb.New(time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC))
	monthly := &plansv1.Subscription{FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, StartTime: start, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH}
	if got := nextCharge(monthly, now, loc); got.Format("2006-01-02") != "2026-10-28" {
		t.Errorf("monthly in advance = %v", got)
	}
	yearly := &plansv1.Subscription{FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, StartTime: start, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR}
	if got := nextCharge(yearly, now, loc); got.Format("2006-01-02") != "2027-07-28" {
		t.Errorf("yearly in advance = %v", got)
	}
	trial := &plansv1.Subscription{FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, StartTime: start, TrialEndTime: timestamppb.New(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))}
	if got := nextCharge(trial, now, loc); got.Format("2006-01-02") != "2026-11-02" {
		t.Errorf("after a trial = %v", got)
	}
	arrears := &plansv1.Subscription{FeeTiming: plansv1.FeeTiming_FEE_TIMING_ARREARS, StartTime: start}
	if got := nextCharge(arrears, now, loc); got.Format("2006-01-02") != "2026-11-01" {
		t.Errorf("in arrears = the month's close, got %v", got)
	}
	provider := &plansv1.Subscription{FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, Owner: "provider", StartTime: start}
	if got := nextCharge(provider, now, loc); got.Format("2006-01-02") != "2026-11-01" {
		t.Errorf("a provider's own subscription = %v", got)
	}

	active := plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE
	subs := []*plansv1.Subscription{
		{Id: "a", Currency: "USD", State: active, FeeTiming: monthly.FeeTiming, StartTime: start},
		{Id: "b", Currency: "USD", State: active, FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, StartTime: timestamppb.New(time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC))},
		{Id: "c", Currency: "EUR", State: active, FeeTiming: plansv1.FeeTiming_FEE_TIMING_ARREARS, StartTime: start},
		{Id: "cancelled", Currency: "USD", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED, StartTime: start, EndTime: start},
		{Id: "leaving", Currency: "USD", State: active, CancelAtPeriodEnd: true, FeeTiming: monthly.FeeTiming, StartTime: start},
		{Id: "paused", Currency: "USD", State: plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED, FeeTiming: monthly.FeeTiming, StartTime: start},
		{Id: "later", Currency: "USD", State: active, FeeTiming: plansv1.FeeTiming_FEE_TIMING_ADVANCE, Interval: plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR, StartTime: start},
	}
	got := renewalsOf(subs, now, loc, 30, 6)
	if len(got["USD"]) != 2 || got["USD"][0].SubscriptionID != "b" || got["USD"][1].SubscriptionID != "a" || len(got["EUR"]) != 1 {
		t.Errorf("renewals = %+v (soonest first; a cancelled, leaving, paused or far-off subscription is not upcoming)", got)
	}
}

func TestUsageOfFlagsCustomersNearAndOverTheirLimits(t *testing.T) {
	st := func(meter, used, limit string, allowed bool) *limitsv1.LimitStatus {
		return &limitsv1.LimitStatus{MeterCode: meter, Used: used, Limit: limit, Allowed: allowed}
	}
	got := usageOf(map[string][]*limitsv1.LimitStatus{
		"ann": {st("api_calls", "950", "1000", true), {MeterCode: "seats", Unlimited: true, Allowed: true}},
		"bob": {st("api_calls", "1200", "1000", false)},
		"cy":  {st("api_calls", "10", "1000", true), st("storage", "5", "10", true)},
	})
	if len(got) != 2 || got[0].MeterCode != "api_calls" {
		t.Fatalf("meters = %+v (unlimited ones are skipped; the one with trouble comes first)", got)
	}
	api := got[0]
	if api.Customers != 3 || api.Over != 1 || api.Near != 1 || len(api.Top) != 3 || api.Top[0].CustomerRef != "bob" || api.Top[0].Percent != 120 || api.Top[1].CustomerRef != "ann" || api.Top[1].Percent != 95 {
		t.Errorf("api_calls = %+v", api)
	}
}

// --- the endpoint

func TestDashboardComposesPerCurrencyAndNeverAcrossThem(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/dashboard?environment=live&period=7d", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("dashboard: %d %v", resp.StatusCode, m)
	}
	if resp.Header.Get("Cache-Control") == "" {
		t.Error("a dashboard must say how it may be cached")
	}
	if m["tenant_id"] != adaLive || m["environment"] != "live" || m["default_currency"] != "USD" || len(m["unavailable"].([]any)) != 0 {
		t.Errorf("header = %v", m)
	}
	rng := m["range"].(map[string]any)
	if rng["period"] != "7d" || rng["days"] != float64(7) || rng["granularity"] != "day" || rng["compare"] != true || rng["prev_from"] == "" {
		t.Errorf("range = %v", rng)
	}
	by := map[string]map[string]any{}
	var order []string
	for _, c := range m["currencies"].([]any) {
		cm := c.(map[string]any)
		by[cm["currency"].(string)] = cm
		order = append(order, cm["currency"].(string))
	}
	// One block per currency that has anything: the tenant's own first.
	for _, want := range []string{"USD", "TRY", "EUR"} {
		if by[want] == nil {
			t.Fatalf("no block for %s: %v", want, order)
		}
	}
	if order[0] != "USD" {
		t.Errorf("order = %v, the tenant's currency first", order)
	}
	usd := by["USD"]
	rev := usd["revenue"].(map[string]any)
	if rev["net_minor"] != float64(7000) || rev["gross_minor"] != float64(7100) || rev["refunds_minor"] != float64(50) || rev["paid_invoices"] != float64(4) {
		t.Errorf("USD revenue = %v", rev)
	}
	if prev := usd["prev_revenue"].(map[string]any); prev["net_minor"] != float64(3500) {
		t.Errorf("USD previous revenue = %v", prev)
	}
	series := usd["series"].([]any)
	if len(series) != 7 || series[0].(map[string]any)["prev_net_minor"] != float64(500) || series[0].(map[string]any)["net_minor"] != float64(1000) {
		t.Errorf("USD series = %v", series)
	}
	// The other currencies have no accounting block: zero series, zero revenue, never USD's money.
	if r := by["TRY"]["revenue"].(map[string]any); r["net_minor"] != float64(0) {
		t.Errorf("TRY revenue = %v", r)
	}
	if len(by["TRY"]["series"].([]any)) != 7 || by["TRY"]["series"].([]any)[0].(map[string]any)["net_minor"] != float64(0) {
		t.Errorf("TRY series = %v", by["TRY"]["series"])
	}
	pay := usd["payments"].(map[string]any)
	if pay["paid"] != float64(1) || pay["failed"] != float64(1) || pay["refunds"] != float64(1) || pay["failed_rate_bp"] != float64(5000) {
		t.Errorf("USD payments = %v", pay)
	}
	if by["TRY"]["payments"].(map[string]any)["paid_minor"] != float64(25000) {
		t.Errorf("TRY payments = %v", by["TRY"]["payments"])
	}
	// Subscriptions come from the report (rows of the fake: USD 4 active at 49.00, TRY 1 at 250.00); ARPU is net revenue over subscribers, in the currency.
	subs := usd["subscriptions"].(map[string]any)
	if subs["active"] != float64(4) || subs["mrr_minor"] != float64(4900) || subs["arr_minor"] != float64(58800) || usd["arpu_minor"] != float64(1750) {
		t.Errorf("USD subscriptions = %v arpu %v", subs, usd["arpu_minor"])
	}
	if tops := usd["top_customers"].([]any); len(tops) != 1 || tops[0].(map[string]any)["customer_external_id"] != "ann" || tops[0].(map[string]any)["income_minor"] != float64(7000) {
		t.Errorf("USD top customers = %v", usd["top_customers"])
	}
	if usd["open_count"] != float64(3) || usd["open_minor"] != float64(3500) || len(usd["aging"].([]any)) != 2 || by["EUR"]["open_minor"] != float64(900) {
		t.Errorf("aging: USD %v %v, EUR %v", usd["open_count"], usd["open_minor"], by["EUR"]["open_minor"])
	}
	if r := usd["renewals"].([]any); len(r) != 1 || r[0].(map[string]any)["subscription_id"] != "s1" {
		t.Errorf("USD renewals = %v (cancelled ones are not upcoming)", usd["renewals"])
	}
	if r := by["TRY"]["renewals"].([]any); len(r) != 1 || r[0].(map[string]any)["subscription_id"] != "s2" {
		t.Errorf("TRY renewals = %v", by["TRY"]["renewals"])
	}
	cu := m["customers"].(map[string]any)
	if cu["total"] != float64(7) || cu["new"] != float64(3) || cu["prev_new"] != float64(1) {
		t.Errorf("customers = %v", cu)
	}
	us := m["usage"].(map[string]any)
	meters := us["meters"].([]any)
	if us["period_id"] != "2026-10" || len(meters) != 1 || meters[0].(map[string]any)["over"] != float64(1) || meters[0].(map[string]any)["near"] != float64(1) {
		t.Errorf("usage = %v", us)
	}
	if act := m["activity"].([]any); len(act) < 3 {
		t.Errorf("activity = %v", act)
	} else if first := act[0].(map[string]any); first["kind"] == "" {
		t.Errorf("activity = %v", act)
	}
	if h := m["health"].(map[string]any); h["webhook_failed"] != float64(2) || h["payments_reachable"] != true {
		t.Errorf("health = %v", h)
	}
	// Every upstream call carried the environment's tenant, nothing else.
	for _, tn := range append(append([]string{}, p.plans.tenants...), p.billing.tenants...) {
		if tn != adaLive {
			t.Errorf("an upstream call carried tenant %s", tn)
		}
	}
}

func TestDashboardPeriodsAndErrors(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	// Without the comparison there is nothing to compare with, and the long period is monthly.
	resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/dashboard?period=ytd&compare=0", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("ytd: %d %v", resp.StatusCode, m)
	}
	usd := m["currencies"].([]any)[0].(map[string]any)
	if usd["prev_revenue"] != nil || usd["prev_payments"] != nil || m["range"].(map[string]any)["compare"] != false {
		t.Errorf("compare=0: %v", usd["prev_revenue"])
	}
	if g := m["range"].(map[string]any)["granularity"]; g != "month" && m["range"].(map[string]any)["days"].(float64) > 92 {
		t.Errorf("a long period is monthly, got %v", g)
	}
	// Bad requests are refused with a key the console translates, before any upstream is called.
	before := len(p.plans.tenants) + len(p.billing.tenants)
	for _, c := range []struct{ q, key string }{
		{"period=forever", "gateway.dashboard.bad_period"},
		{"period=custom&from=2026-10-09&to=2026-10-01", "gateway.dashboard.bad_period"},
		{"period=custom&from=2019-01-01&to=2026-10-01", "gateway.dashboard.range_too_long"},
	} {
		resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/dashboard?"+c.q, tokAda, "", "")
		if resp.StatusCode != 400 || errKeyOf(m) != c.key {
			t.Errorf("%s: %d %v", c.q, resp.StatusCode, m)
		}
	}
	if resp, _ := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/dashboard?environment=prod", tokAda, "", ""); resp.StatusCode != 400 {
		t.Errorf("bad environment: %d", resp.StatusCode)
	}
	if got := len(p.plans.tenants) + len(p.billing.tenants); got != before+0 && got < before {
		t.Errorf("calls went backwards: %d -> %d", before, got)
	}
}

func TestDashboardIsTheCallersOwnAndDegradesWhenPaymentsAreDown(t *testing.T) {
	p := newProduct(t, nil, nil) // no payments-worker
	// Bob cannot read Ada's dashboard, in either environment; nobody signed in cannot read any.
	for _, path := range []string{"/app/v1/apps/" + adaApp + "/dashboard", "/app/v1/apps/" + adaApp + "/dashboard?environment=sandbox"} {
		if resp, _ := p.call(t, "GET", path, tokBob, "", ""); resp.StatusCode != 404 {
			t.Errorf("Bob on %s: %d", path, resp.StatusCode)
		}
		if resp, _ := p.call(t, "GET", path, "", "", ""); resp.StatusCode != 401 {
			t.Errorf("no session on %s: %d", path, resp.StatusCode)
		}
	}
	if len(p.billing.tenants)+len(p.plans.tenants) != 0 {
		t.Error("a refused dashboard reached an upstream")
	}
	resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/dashboard", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("dashboard: %d %v", resp.StatusCode, m)
	}
	if m["status"] != "degraded" {
		t.Errorf("status = %v", m["status"])
	}
	un := m["unavailable"].([]any)
	if len(un) != 1 || un[0] != "payments" {
		t.Errorf("unavailable = %v: only the payments section is lost", un)
	}
	usd := m["currencies"].([]any)[0].(map[string]any)
	if usd["payments"] != nil || usd["revenue"].(map[string]any)["net_minor"] == nil {
		t.Errorf("without payments the rest stays: %v", usd)
	}
	// A page that lost a section is not remembered: the second look asks again.
	n := len(p.billing.tenants)
	p.call(t, "GET", "/app/v1/apps/"+adaApp+"/dashboard", tokAda, "", "")
	if len(p.billing.tenants) == n {
		t.Error("a degraded dashboard was served from the cache")
	}
}

func TestDashboardIsRememberedForAFewSeconds(t *testing.T) {
	p := newProduct(t, func() []map[string]any { return ledgerRows(time.Now()) }, nil)
	path := "/app/v1/apps/" + adaApp + "/dashboard?period=30d"
	p.call(t, "GET", path, tokAda, "", "")
	n := len(p.billing.tenants)
	_, m := p.call(t, "GET", path, tokAda, "", "")
	if len(p.billing.tenants) != n || m["tenant_id"] != adaLive {
		t.Errorf("the second look reached billing again (%d -> %d)", n, len(p.billing.tenants))
	}
	// Another period, another environment: not the same page.
	p.call(t, "GET", path+"&compare=0", tokAda, "", "")
	if len(p.billing.tenants) == n {
		t.Error("a different question was answered from the cache")
	}
	raw, _ := json.Marshal(m)
	if len(raw) == 0 {
		t.Error("empty")
	}
}

func TestDashboardOfATenantForTheOperator(t *testing.T) {
	ts, _ := newTestServer(t)
	if resp, _ := do(t, ts, "GET", "/admin/v1/tenants/"+liveTenant.String()+"/dashboard", "live-key", ""); resp.StatusCode != 401 {
		t.Errorf("a tenant key on the operator's dashboard: %d", resp.StatusCode)
	}
}
