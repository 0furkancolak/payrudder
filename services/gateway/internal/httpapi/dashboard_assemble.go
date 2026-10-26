package httpapi

import (
	"cmp"
	"slices"
	"sort"
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// dashInput is everything the dashboard is put together from: what the services answered, which sections did not, and the period.
type dashInput struct {
	appID, envName      string
	tenant              *plansv1.Tenant
	per                 dashPeriod
	loc                 *time.Location
	now                 time.Time
	curMonth, prevMonth time.Time // the months the subscription reports are for
	src                 *dashSources
	down                sectionsDown
}

// dashBuilder puts the dashboard together: one block per currency, the page-wide figures, the activity feed.
type dashBuilder struct {
	in     dashInput
	o      *dashboardResponse
	blocks map[string]*dashCurrency
}

// assembleDashboard is the pure part of the dashboard: it reads nothing, it only sorts what the sections answered into the page. A section in `down`
// is left out and named in `unavailable`.
func assembleDashboard(in dashInput) *dashboardResponse {
	t := in.tenant
	b := &dashBuilder{in: in, blocks: map[string]*dashCurrency{}, o: &dashboardResponse{AppID: in.appID, Environment: in.envName, TenantID: t.GetId(), Timezone: t.GetTimezone(),
		DefaultCurrency: t.GetCurrency(), Range: in.per.rangeJSON(), GeneratedAt: in.now.UTC(), Unavailable: []string{}, Currencies: []*dashCurrency{}, Activity: []activityItem{}}}
	b.o.Unavailable = append(b.o.Unavailable, in.down...)
	b.block(t.GetCurrency())
	net, prevNet := b.revenue()
	b.invoices()
	b.subscriptions()
	if in.down.has("customers") {
		c := in.src.customers
		b.o.Customers = &dashCustomers{Total: c.GetTotal(), New: c.GetNewInRange(), PrevNew: c.GetNewInPrevious()}
	}
	b.payments()
	b.renewals()
	b.usage()
	b.invoiceActivity()
	b.subscriptionActivity()
	b.series(net, prevNet)
	slices.SortStableFunc(b.o.Activity, func(x, y activityItem) int { return y.At.Compare(x.At) })
	if len(b.o.Activity) > dashActivityMax {
		b.o.Activity = b.o.Activity[:dashActivityMax]
	}
	b.currencies()
	b.o.Health = newHealth(in.src.paymentsUp, in.src.providers)
	if in.down.has("webhooks") {
		b.o.Health.WebhookEndpoints, b.o.Health.WebhookFailed, b.o.Health.WebhookFailedMore = in.src.endpoints, in.src.failed, in.src.failedMore
	}
	b.o.Status = dashboardStatus(b.o)
	return b.o
}

func (b *dashBuilder) block(currency string) *dashCurrency {
	if b.blocks[currency] == nil {
		b.blocks[currency] = &dashCurrency{Currency: currency, Series: []dashPoint{}, TopCustomers: []dashTopCustomer{}, TopPackages: []dashPackage{}, Aging: []dashAging{}, Renewals: []dashRenewal{}}
	}
	return b.blocks[currency]
}

// revenue fills the revenue of each currency and of the period before it, and returns their net income by bucket (currency, then bucket) for the series.
func (b *dashBuilder) revenue() (net, prevNet map[string]map[string]int64) {
	net, prevNet = map[string]map[string]int64{}, map[string]map[string]int64{}
	if !b.in.down.has("revenue") {
		return net, prevNet
	}
	for _, c := range b.in.src.cur.GetCurrencies() {
		b.block(c.GetCurrency()).Revenue = revenueOf(c)
		net[c.GetCurrency()] = map[string]int64{}
		for _, p := range c.GetSeries() {
			net[c.GetCurrency()][p.GetBucket()] = p.GetNetIncomeMinor()
		}
	}
	for _, c := range b.in.src.prev.GetCurrencies() {
		rv := revenueOf(c)
		b.block(c.GetCurrency()).PrevRevenue = &rv
		prevNet[c.GetCurrency()] = map[string]int64{}
		for _, p := range c.GetSeries() {
			prevNet[c.GetCurrency()][p.GetBucket()] = p.GetNetIncomeMinor()
		}
	}
	return net, prevNet
}

// invoices fills the top customers and the ageing of open invoices of each currency.
func (b *dashBuilder) invoices() {
	if !b.in.down.has("invoices") {
		return
	}
	for _, c := range b.in.src.stats.GetCurrencies() {
		blk := b.block(c.GetCurrency())
		for _, tc := range c.GetTopCustomers() {
			blk.TopCustomers = append(blk.TopCustomers, dashTopCustomer{CustomerID: tc.GetCustomerId(), ExternalID: tc.GetCustomerExternalId(), Name: tc.GetCustomerName(), IncomeMinor: tc.GetIncomeMinor(), Invoices: tc.GetInvoices()})
		}
		for _, a := range c.GetAging() {
			blk.Aging = append(blk.Aging, dashAging{Bucket: a.GetBucket(), Count: a.GetCount(), AmountMinor: a.GetAmountMinor()})
			blk.OpenCount += a.GetCount()
			blk.OpenMinor += a.GetAmountMinor()
		}
	}
}

// subscriptions fills the subscription figures of each currency, for the month the period ends in and the month before it, and its top packages.
func (b *dashBuilder) subscriptions() {
	if !b.in.down.has("subscriptions") {
		return
	}
	src := b.in.src
	for c, v := range subsOf(src.subsNow.GetRows(), b.in.curMonth.Format(monthLayout)) {
		b.block(c).Subscriptions = v
	}
	if b.in.per.Compare {
		for c, v := range subsOf(src.subsPrev.GetRows(), b.in.prevMonth.Format(monthLayout)) {
			b.block(c).PrevSubscriptions = v
		}
	}
	for c, list := range topPackages(src.subsNow.GetRows()) {
		b.block(c).TopPackages = list
	}
}

// payments folds the ledger into the payments of the period and of the one before it, and adds the newest to the activity feed.
func (b *dashBuilder) payments() {
	if !b.in.down.has("payments") {
		return
	}
	per, ledger := b.in.per, b.in.src.ledger
	folded := foldLedger(ledger)
	oldest := b.in.now
	for _, r := range ledger {
		if r.OccurredAt.Before(oldest) {
			oldest = r.OccurredAt
		}
	}
	full := len(ledger) >= ledgerWindowRows
	b.o.PaymentsTruncated = full && !oldest.Before(per.From)
	b.o.PaymentsPrevCovered = !full || oldest.Before(per.PrevFrom)
	for c, v := range paymentsByCurrency(folded, per.From, per.To) {
		b.block(c).Payments = v
	}
	if per.Compare && b.o.PaymentsPrevCovered {
		for c, v := range paymentsByCurrency(folded, per.PrevFrom, per.PrevTo) {
			b.block(c).PrevPayments = v
		}
	}
	b.o.Activity = append(b.o.Activity, paymentActivity(folded, dashActivityPayments)...)
}

// renewals fills the upcoming charges of each currency.
func (b *dashBuilder) renewals() {
	if !b.in.down.has("renewals") {
		return
	}
	b.o.RenewalsTruncated = b.in.src.renewals.GetNextPageToken() != ""
	for c, list := range renewalsOf(b.in.src.renewals.GetSubscriptions(), b.in.now, b.in.loc, dashRenewalHorizonDays, dashRenewalsPerCurrency) {
		b.block(c).Renewals = list
	}
}

// usage fills the usage against limits of the sampled customers.
func (b *dashBuilder) usage() {
	if !b.in.down.has("usage") {
		return
	}
	src := b.in.src
	b.o.Usage = &dashUsage{PeriodID: src.usagePeriod, Sampled: src.usageSampled, Meters: usageOf(src.usageByCustomer)}
	if b.o.Usage.Meters == nil {
		b.o.Usage.Meters = []usageMeter{}
	}
}

// invoiceActivity adds the newest invoices to the activity feed.
func (b *dashBuilder) invoiceActivity() {
	if !b.in.down.has("invoices") {
		return
	}
	for _, i := range b.in.src.stats.GetRecentInvoices() {
		amt := i.GetTotalMinor()
		label := i.GetNumber()
		if label == "" {
			label = "draft"
		}
		b.o.Activity = append(b.o.Activity, activityItem{Kind: "invoice", At: i.GetCreateTime().AsTime(), Ref: i.GetId(), Label: label, Customer: i.GetCustomerExternalId(), Status: i.GetStatus(), AmountMinor: &amt, Currency: i.GetCurrency()})
	}
}

// subscriptionActivity adds the newest subscriptions to the activity feed.
func (b *dashBuilder) subscriptionActivity() {
	if !b.in.down.has("activity") {
		return
	}
	for _, sub := range b.in.src.recentSubs.GetSubscriptions() {
		b.o.Activity = append(b.o.Activity, activityItem{Kind: "subscription", At: sub.GetCreateTime().AsTime(), Ref: sub.GetId(), Label: sub.GetPlanCode(), Customer: sub.GetCustomerExternalId(), Status: stateWord(sub.GetState()), Currency: sub.GetCurrency()})
	}
}

// series fills the revenue series of each currency, bucket by bucket, next to the one of the period before it.
func (b *dashBuilder) series(net, prevNet map[string]map[string]int64) {
	if !b.in.down.has("revenue") {
		return
	}
	per := b.in.per
	buckets := dashBuckets(per.From, per.To, per.Granularity)
	prevBuckets := dashBuckets(per.PrevFrom, per.PrevTo, per.Granularity)
	for code, blk := range b.blocks {
		if per.Compare && blk.PrevRevenue == nil {
			blk.PrevRevenue = &dashRevenue{}
		}
		blk.Series = make([]dashPoint, 0, len(buckets))
		for i, d := range buckets {
			pt := dashPoint{Date: d, NetMinor: net[code][d]}
			if per.Compare && i < len(prevBuckets) {
				v := prevNet[code][prevBuckets[i]]
				pt.PrevDate, pt.PrevNetMinor = prevBuckets[i], &v
			}
			blk.Series = append(blk.Series, pt)
		}
	}
}

// currencies orders the blocks: the tenant's own currency first, then by net revenue, biggest first. A currency is never ranked against another's money by
// value alone; only the order of the tabs is decided here, and it is the order a person would want (their own currency, then where the money is).
func (b *dashBuilder) currencies() {
	for _, blk := range b.blocks {
		if blk.Subscriptions != nil || blk.PrevSubscriptions != nil {
			if blk.Subscriptions != nil {
				blk.ArpuMinor = arpu(blk.Revenue.NetMinor, blk.Subscriptions)
			}
			if blk.PrevRevenue != nil && blk.PrevSubscriptions != nil {
				blk.PrevArpuMinor = arpu(blk.PrevRevenue.NetMinor, blk.PrevSubscriptions)
			}
		}
		b.o.Currencies = append(b.o.Currencies, blk)
	}
	own := b.in.tenant.GetCurrency()
	sort.SliceStable(b.o.Currencies, func(i, j int) bool {
		x, y := b.o.Currencies[i], b.o.Currencies[j]
		if (x.Currency == own) != (y.Currency == own) {
			return x.Currency == own
		}
		if x.Revenue.NetMinor != y.Revenue.NetMinor {
			return x.Revenue.NetMinor > y.Revenue.NetMinor
		}
		return x.Currency < y.Currency
	})
}

// topPackages ranks the plans of each currency by recurring revenue and keeps the first dashTopPackages.
func topPackages(rows []*plansv1.SubscriptionReportRow) map[string][]dashPackage {
	byPlan := map[string][]dashPackage{}
	for _, r := range rows {
		if live := r.GetActive() + r.GetTrialing() + r.GetPastDue(); live > 0 {
			byPlan[r.GetCurrency()] = append(byPlan[r.GetCurrency()], dashPackage{PlanCode: r.GetPlanCode(), Active: live, MRRMinor: r.GetMrrMinor()})
		}
	}
	for c, list := range byPlan {
		slices.SortStableFunc(list, func(a, b dashPackage) int {
			if x := cmp.Compare(b.MRRMinor, a.MRRMinor); x != 0 {
				return x
			}
			return cmp.Compare(b.Active, a.Active)
		})
		byPlan[c] = list[:min(dashTopPackages, len(list))]
	}
	return byPlan
}
