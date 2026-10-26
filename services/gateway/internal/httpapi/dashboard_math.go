package httpapi

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"time"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	limitsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/limits/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

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
			} else if pct >= dashUsageNearPercent {
				a.m.Near++
			}
			a.rows = append(a.rows, usageCustomer{CustomerRef: ref, Used: st.GetUsed(), Limit: st.GetLimit(), Percent: pct})
		}
	}
	out := make([]usageMeter, 0, len(meters))
	for _, a := range meters {
		slices.SortStableFunc(a.rows, func(x, y usageCustomer) int { return cmp.Compare(y.Percent, x.Percent) })
		a.m.Top = a.rows[:min(dashUsageTop, len(a.rows))]
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

// usagePick is a customer whose limits the dashboard reads.
type usagePick struct{ id, ref string }

// usageSample picks up to max customers from the subscriptions, each once, those that are live (active, trialing or past due and not ended).
func usageSample(subs []*plansv1.Subscription, max int) []usagePick {
	seen := map[string]bool{}
	var picks []usagePick
	for _, sub := range subs {
		st := sub.GetState()
		if sub.GetEndTime() != nil || seen[sub.GetCustomerId()] || (st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE && st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING && st != plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE) {
			continue
		}
		seen[sub.GetCustomerId()] = true
		if picks = append(picks, usagePick{sub.GetCustomerId(), sub.GetCustomerExternalId()}); len(picks) == max {
			break
		}
	}
	return picks
}
