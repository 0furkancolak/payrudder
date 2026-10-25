// Package query decides what promoting an app's sandbox configuration to its live environment would do (phase 45 D). It is pure: two sets of configuration go
// in, a decision per object comes out, and nothing is read or written here. Every object is matched by a stable key (a meter code, a plan code, a tax country,
// a coupon code, a webhook URL), so promoting twice does nothing the second time. Promotion only adds: an object that exists in live and differs is a conflict, shown
// and left alone.
package query

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// The families, in the order they are applied (a plan's prices need its meters first).
const (
	FamilyMeters   = "meters"
	FamilyPlans    = "plans"
	FamilyTaxes    = "taxes"
	FamilyCoupons  = "coupons"
	FamilyMail     = "mail"
	FamilyWebhooks = "webhooks"
)

// Decision is what promotion does about one object. Item is the sandbox object a create is made from.
type Decision struct {
	Key    string
	Action plansv1.PromoteChange_Action
	Detail string
	Item   any
}

func create(key string, item any) Decision {
	return Decision{Key: key, Action: plansv1.PromoteChange_ACTION_CREATE, Item: item}
}
func same(key string) Decision {
	return Decision{Key: key, Action: plansv1.PromoteChange_ACTION_UNCHANGED}
}
func conflict(key, detail string) Decision {
	return Decision{Key: key, Action: plansv1.PromoteChange_ACTION_CONFLICT, Detail: detail}
}

// Meters compares the latest version of each meter by code.
func Meters(sandbox, live []*plansv1.Meter) []Decision {
	byCode := map[string]*plansv1.Meter{}
	for _, m := range live {
		byCode[m.GetCode()] = m
	}
	var out []Decision
	for _, m := range sortedBy(sandbox, func(m *plansv1.Meter) string { return m.GetCode() }) {
		l, ok := byCode[m.GetCode()]
		switch {
		case !ok:
			out = append(out, create(m.GetCode(), m))
		case meterTerms(m) == meterTerms(l):
			out = append(out, same(m.GetCode()))
		default:
			out = append(out, conflict(m.GetCode(), "sandbox: "+meterTerms(m)+"; live: "+meterTerms(l)))
		}
	}
	return out
}

// meterTerms is what makes two meters the same meter: the part of Meter that is not an id, a version or a time.
func meterTerms(m *plansv1.Meter) string {
	f := &plansv1.Meter{Aggregation: m.GetAggregation(), ValueProperty: m.GetValueProperty(), Filter: m.GetFilter(), QuantityScale: m.GetQuantityScale(), UnitLabel: m.GetUnitLabel()}
	return fmt.Sprintf("%s %s scale %d unit %q filter %s", f.GetAggregation(), f.GetValueProperty(), f.GetQuantityScale(), f.GetUnitLabel(), canonical(f.GetFilter()))
}

func canonical(filter map[string]*plansv1.FilterValues) string {
	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := slices.Clone(filter[k].GetValues())
		sort.Strings(vals)
		parts = append(parts, k+"="+strings.Join(vals, "|"))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// PlanSet is a plan with its latest published version (nil Version: nothing published yet).
type PlanSet struct {
	Plan    *plansv1.Plan
	Version *plansv1.PlanVersion
}

// Plans compares plans by code. A sandbox plan with nothing published is not promoted: there is nothing to price. A plan live has, whose latest version is not the
// sandbox's, is a conflict: the sandbox may be newer or older, and which one is right is the person's to say, in live.
func Plans(sandbox, live []PlanSet) []Decision {
	byCode := map[string]PlanSet{}
	for _, p := range live {
		byCode[p.Plan.GetCode()] = p
	}
	var out []Decision
	for _, p := range sortedBy(sandbox, func(p PlanSet) string { return p.Plan.GetCode() }) {
		code := p.Plan.GetCode()
		if p.Version == nil {
			out = append(out, Decision{Key: code, Action: plansv1.PromoteChange_ACTION_SKIPPED, Detail: "no version published in the sandbox"})
			continue
		}
		l, ok := byCode[code]
		switch {
		case !ok:
			out = append(out, create(code, p))
		case l.Version == nil:
			out = append(out, create(code, p)) // the plan exists in live without a version: the version is what is added
		case l.Plan.GetName() != p.Plan.GetName():
			out = append(out, conflict(code, fmt.Sprintf("name: sandbox %q; live %q", p.Plan.GetName(), l.Plan.GetName())))
		case !proto.Equal(versionTerms(p.Version), versionTerms(l.Version)):
			out = append(out, conflict(code, fmt.Sprintf("the latest version differs (sandbox v%d, live v%d): publish the version you want in live", p.Version.GetVersion(), l.Version.GetVersion())))
		default:
			out = append(out, same(code))
		}
	}
	return out
}

// versionTerms is a plan version without its ids, number and time, with every list in a fixed order, so that two versions with the same terms are equal.
func versionTerms(v *plansv1.PlanVersion) *plansv1.PlanVersion {
	c := proto.Clone(v).(*plansv1.PlanVersion)
	c.Id, c.PlanId, c.PlanCode, c.Version, c.PublishTime = "", "", "", 0, nil
	sortPrices(c.Prices)
	sort.Slice(c.Entitlements, func(i, j int) bool { return c.Entitlements[i].GetMeterCode() < c.Entitlements[j].GetMeterCode() })
	sort.Slice(c.Fees, func(i, j int) bool {
		if c.Fees[i].GetCurrency() != c.Fees[j].GetCurrency() {
			return c.Fees[i].GetCurrency() < c.Fees[j].GetCurrency()
		}
		return c.Fees[i].GetInterval() < c.Fees[j].GetInterval()
	})
	sort.Slice(c.CurrencyPrices, func(i, j int) bool { return c.CurrencyPrices[i].GetCurrency() < c.CurrencyPrices[j].GetCurrency() })
	for _, cp := range c.CurrencyPrices {
		sortPrices(cp.Prices)
	}
	return c
}

func sortPrices(p []*plansv1.Price) {
	sort.Slice(p, func(i, j int) bool { return p[i].GetMeterCode() < p[j].GetMeterCode() })
}

// PublishRequest is what publishing a sandbox plan version in live takes.
func PublishRequest(code string, v *plansv1.PlanVersion) *plansv1.PublishPlanVersionRequest {
	return &plansv1.PublishPlanVersionRequest{PlanCode: code, BaseFeeMinor: v.GetBaseFeeMinor(), Prices: v.GetPrices(), Entitlements: v.GetEntitlements(),
		Fees: v.GetFees(), CurrencyPrices: v.GetCurrencyPrices()}
}

// Taxes compares tax rates by country ("" is the rate for every other country).
func Taxes(sandbox, live []*plansv1.TaxRate) []Decision {
	byCountry := map[string]*plansv1.TaxRate{}
	for _, r := range live {
		byCountry[r.GetCountry()] = r
	}
	var out []Decision
	for _, r := range sortedBy(sandbox, func(r *plansv1.TaxRate) string { return r.GetCountry() }) {
		key := r.GetCountry()
		if key == "" {
			key = "*"
		}
		l, ok := byCountry[r.GetCountry()]
		switch {
		case !ok:
			out = append(out, create(key, r))
		case l.GetName() == r.GetName() && l.GetRateBps() == r.GetRateBps():
			out = append(out, same(key))
		default:
			out = append(out, conflict(key, fmt.Sprintf("sandbox: %s %d bp; live: %s %d bp", r.GetName(), r.GetRateBps(), l.GetName(), l.GetRateBps())))
		}
	}
	return out
}

// Coupons compares coupons by code on their terms. An inactive sandbox coupon is not promoted. Counters, ids and times are not terms.
func Coupons(sandbox, live []*plansv1.Coupon) []Decision {
	byCode := map[string]*plansv1.Coupon{}
	for _, c := range live {
		byCode[c.GetCode()] = c
	}
	var out []Decision
	for _, c := range sortedBy(sandbox, func(c *plansv1.Coupon) string { return c.GetCode() }) {
		l, ok := byCode[c.GetCode()]
		switch {
		case !c.GetActive() && !ok:
			out = append(out, Decision{Key: c.GetCode(), Action: plansv1.PromoteChange_ACTION_SKIPPED, Detail: "inactive in the sandbox"})
		case !ok:
			out = append(out, create(c.GetCode(), c))
		case couponTerms(c) == couponTerms(l):
			out = append(out, same(c.GetCode()))
		default:
			out = append(out, conflict(c.GetCode(), "sandbox: "+couponTerms(c)+"; live: "+couponTerms(l)))
		}
	}
	return out
}

func couponTerms(c *plansv1.Coupon) string {
	targets := make([]string, 0, len(c.GetAppliesTo()))
	for _, t := range c.GetAppliesTo() {
		targets = append(targets, t.String())
	}
	sort.Strings(targets)
	expire := ""
	if c.GetExpireTime() != nil {
		expire = c.GetExpireTime().AsTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	return fmt.Sprintf("%q %d bp %d %s %s x%d max %d bonus %d expires %q for %s", c.GetName(), c.GetPercentBps(), c.GetAmountMinor(), c.GetCurrency(), c.GetDuration(),
		c.GetDurationPeriods(), c.GetMaxRedemptions(), c.GetBonusPercentBps(), expire, strings.Join(targets, ","))
}

// Mail compares the mail categories switched off. A live environment that never switched anything off takes the sandbox's choice; one that did and differs is a
// conflict. The two are one object, "settings".
func Mail(sandbox, live []*plansv1.MailCategorySetting) []Decision {
	off := func(s []*plansv1.MailCategorySetting) []string {
		var d []string
		for _, c := range s {
			if !c.GetEnabled() {
				d = append(d, c.GetCategory())
			}
		}
		sort.Strings(d)
		return d
	}
	sb, lv := off(sandbox), off(live)
	switch {
	case slices.Equal(sb, lv):
		return []Decision{same("settings")}
	case len(lv) == 0:
		return []Decision{create("settings", sandbox)}
	default:
		return []Decision{conflict("settings", fmt.Sprintf("switched off in the sandbox: %v; in live: %v", sb, lv))}
	}
}

// Webhooks compares endpoints by URL. A new endpoint gets a new signing secret; nothing of the sandbox's secret is copied. active says whether live may take endpoints
// (an inactive live environment may not).
func Webhooks(sandbox, live []*plansv1.WebhookEndpoint, liveActive bool) []Decision {
	byURL := map[string]*plansv1.WebhookEndpoint{}
	for _, e := range live {
		byURL[e.GetUrl()] = e
	}
	events := func(e *plansv1.WebhookEndpoint) string {
		t := slices.Clone(e.GetEventTypes())
		sort.Strings(t)
		return strings.Join(t, ",")
	}
	var out []Decision
	for _, e := range sortedBy(sandbox, func(e *plansv1.WebhookEndpoint) string { return e.GetUrl() }) {
		l, ok := byURL[e.GetUrl()]
		switch {
		case ok && events(l) == events(e):
			out = append(out, same(e.GetUrl()))
		case ok:
			out = append(out, conflict(e.GetUrl(), fmt.Sprintf("event types: sandbox [%s]; live [%s]", events(e), events(l))))
		case !liveActive:
			out = append(out, Decision{Key: e.GetUrl(), Action: plansv1.PromoteChange_ACTION_SKIPPED, Detail: "the live environment is not active yet: webhook endpoints are added after go-live"})
		default:
			out = append(out, create(e.GetUrl(), e))
		}
	}
	return out
}

func sortedBy[T any](in []T, key func(T) string) []T {
	out := slices.Clone(in)
	sort.SliceStable(out, func(i, j int) bool { return key(out[i]) < key(out[j]) })
	return out
}
