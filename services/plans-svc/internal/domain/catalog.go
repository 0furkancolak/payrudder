package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/quantity"
)

var (
	meterCodeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	planCodeRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	propertyRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
)

// AggregationToDB maps the API enum to the stored name.
func AggregationToDB(a plansv1.Aggregation) (string, bool) {
	switch a {
	case plansv1.Aggregation_AGGREGATION_SUM:
		return "SUM", true
	case plansv1.Aggregation_AGGREGATION_COUNT:
		return "COUNT", true
	case plansv1.Aggregation_AGGREGATION_MAX:
		return "MAX", true
	default:
		return "", false
	}
}

// AggregationFromDB maps a stored name to the API enum.
func AggregationFromDB(s string) plansv1.Aggregation {
	switch s {
	case "SUM":
		return plansv1.Aggregation_AGGREGATION_SUM
	case "COUNT":
		return plansv1.Aggregation_AGGREGATION_COUNT
	case "MAX":
		return plansv1.Aggregation_AGGREGATION_MAX
	default:
		return plansv1.Aggregation_AGGREGATION_UNSPECIFIED
	}
}

// MeterInput is a validated CreateMeter request.
type MeterInput struct {
	Code          string
	Aggregation   string
	ValueProperty *string
	Filter        json.RawMessage
	Scale         int16
	UnitLabel     string
}

// ValidateMeter checks CreateMeter. previousScale is the scale of the code's latest version, or -1 for a new code.
func ValidateMeter(req *plansv1.CreateMeterRequest, previousScale int) (MeterInput, error) {
	var v Violations
	in := MeterInput{Code: req.GetCode(), UnitLabel: req.GetUnitLabel(), Scale: int16(min(req.GetQuantityScale(), 99))}
	if !meterCodeRe.MatchString(in.Code) {
		v.Add("code", "lowercase letters, digits and _, starting with a letter, at most 63 characters")
	}
	agg, ok := AggregationToDB(req.GetAggregation())
	if !ok {
		v.Add("aggregation", "one of SUM, COUNT, MAX")
	}
	in.Aggregation = agg
	switch {
	case agg == "COUNT" && req.GetValueProperty() != "":
		v.Add("value_property", "must be empty for COUNT")
	case agg != "COUNT" && agg != "" && !propertyRe.MatchString(req.GetValueProperty()):
		v.Add("value_property", "required for SUM and MAX: the event property holding the quantity")
	case agg != "COUNT":
		vp := req.GetValueProperty()
		in.ValueProperty = &vp
	}
	if req.GetQuantityScale() > quantity.MaxScale {
		v.Add("quantity_scale", fmt.Sprintf("between 0 and %d", quantity.MaxScale))
	} else if previousScale >= 0 && int(req.GetQuantityScale()) != previousScale {
		v.Add("quantity_scale", fmt.Sprintf("is fixed per meter code (existing versions use %d); use a new code", previousScale))
	}
	if n := len(in.UnitLabel); n == 0 || n > 32 {
		v.Add("unit_label", "required, at most 32 characters (e.g. tokens, GB-h)")
	}
	filter := map[string][]string{}
	if len(req.GetFilter()) > 10 {
		v.Add("filter", "at most 10 properties")
	}
	for k, vals := range req.GetFilter() {
		if !propertyRe.MatchString(k) {
			v.Add("filter."+k, "invalid property name")
			continue
		}
		if n := len(vals.GetValues()); n == 0 || n > 50 {
			v.Add("filter."+k, "1..50 values")
			continue
		}
		sorted := slices.Clone(vals.GetValues())
		slices.Sort(sorted)
		filter[k] = slices.Compact(sorted)
	}
	in.Filter, _ = json.Marshal(filter)
	return in, v.Err()
}

// FilterFromDB decodes the stored filter.
func FilterFromDB(raw json.RawMessage) (map[string]*plansv1.FilterValues, error) {
	var m map[string][]string
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
	}
	out := make(map[string]*plansv1.FilterValues, len(m))
	for k, v := range m {
		out[k] = &plansv1.FilterValues{Values: v}
	}
	return out, nil
}

// ValidatePlan checks CreatePlan.
func ValidatePlan(req *plansv1.CreatePlanRequest) error {
	var v Violations
	if !planCodeRe.MatchString(req.GetCode()) {
		v.Add("code", "lowercase letters, digits, _ and -, starting with a letter, at most 63 characters")
	}
	if n := len(req.GetName()); n == 0 || n > 200 {
		v.Add("name", "required, at most 200 characters")
	}
	return v.Err()
}

// Stored price configs (plan_prices.config). Quantities are scaled units of the meter.
type (
	perUnitConfig struct {
		UnitPrice string `json:"unit_price"`
	}
	tierConfig struct {
		UpToUnits    *int64 `json:"up_to_units"`
		UnitPrice    string `json:"unit_price"`
		FlatFeeMinor int64  `json:"flat_fee_minor"`
	}
	tieredConfig struct {
		Tiers []tierConfig `json:"tiers"`
	}
	packageConfig struct {
		PackageSizeUnits  int64 `json:"package_size_units"`
		PackagePriceMinor int64 `json:"package_price_minor"`
	}
)

// StoredPrice is a validated price ready to insert.
type StoredPrice struct {
	MeterCode string
	Model     string
	Config    json.RawMessage
}

// StoredEntitlement is a validated entitlement ready to insert.
type StoredEntitlement struct {
	MeterCode  string
	LimitUnits int64
	Thresholds []int32
}

// PlanVersionInput is a validated PublishPlanVersion request.
type PlanVersionInput struct {
	BaseFeeMinor int64
	Prices       []StoredPrice
	Entitlements []StoredEntitlement
}

// ValidatePlanVersion checks PublishPlanVersion. scales maps each known meter code to its quantity_scale.
func ValidatePlanVersion(req *plansv1.PublishPlanVersionRequest, scales map[string]int) (PlanVersionInput, error) {
	var v Violations
	in := PlanVersionInput{BaseFeeMinor: req.GetBaseFeeMinor()}
	if in.BaseFeeMinor < 0 {
		v.Add("base_fee_minor", "must be ≥ 0")
	}
	if len(req.GetPrices()) == 0 && in.BaseFeeMinor == 0 {
		v.Add("prices", "a plan version needs at least one price or a base fee")
	}
	seen := map[string]bool{}
	for i, p := range req.GetPrices() {
		field := fmt.Sprintf("prices[%d]", i)
		scale, ok := scales[p.GetMeterCode()]
		if !ok {
			v.Add(field+".meter_code", "unknown meter")
			continue
		}
		if seen[p.GetMeterCode()] {
			v.Add(field+".meter_code", "duplicate meter in prices")
			continue
		}
		seen[p.GetMeterCode()] = true
		sp, err := priceToStored(p, scale, field, &v)
		if err == nil {
			in.Prices = append(in.Prices, sp)
		}
	}
	seenEnt := map[string]bool{}
	for i, e := range req.GetEntitlements() {
		field := fmt.Sprintf("entitlements[%d]", i)
		scale, ok := scales[e.GetMeterCode()]
		if !ok {
			v.Add(field+".meter_code", "unknown meter")
			continue
		}
		if seenEnt[e.GetMeterCode()] {
			v.Add(field+".meter_code", "duplicate meter in entitlements")
			continue
		}
		seenEnt[e.GetMeterCode()] = true
		units, err := quantity.ParseUnits(e.GetLimit(), scale)
		if err != nil {
			v.Add(field+".limit", quantityMessage(err, scale))
			continue
		}
		th := e.GetAlertThresholdsPct()
		if len(th) == 0 {
			th = []int32{80, 100}
		}
		if !strictlyAscending(th) || th[0] < 1 || th[len(th)-1] > 1000 || len(th) > 10 {
			v.Add(field+".alert_thresholds_pct", "1..10 strictly ascending percentages between 1 and 1000")
			continue
		}
		in.Entitlements = append(in.Entitlements, StoredEntitlement{MeterCode: e.GetMeterCode(), LimitUnits: units, Thresholds: th})
	}
	return in, v.Err()
}

var errSkip = errors.New("invalid")

func priceToStored(p *plansv1.Price, scale int, field string, v *Violations) (StoredPrice, error) {
	sp := StoredPrice{MeterCode: p.GetMeterCode()}
	var cfg any
	switch m := p.GetModel().(type) {
	case *plansv1.Price_PerUnit:
		if err := money.ValidateUnitPrice(m.PerUnit.GetUnitPrice()); err != nil {
			v.Add(field+".per_unit.unit_price", err.Error())
			return sp, errSkip
		}
		sp.Model, cfg = "PER_UNIT", perUnitConfig{UnitPrice: m.PerUnit.GetUnitPrice()}
	case *plansv1.Price_Tiered:
		tiers := m.Tiered.GetTiers()
		if len(tiers) == 0 || len(tiers) > 20 {
			v.Add(field+".tiered.tiers", "1..20 tiers")
			return sp, errSkip
		}
		out := tieredConfig{}
		var prev int64 = -1
		ok := true
		for j, t := range tiers {
			tf := fmt.Sprintf("%s.tiered.tiers[%d]", field, j)
			last := j == len(tiers)-1
			tc := tierConfig{UnitPrice: t.GetUnitPrice(), FlatFeeMinor: t.GetFlatFeeMinor()}
			switch {
			case t.GetUpTo() == "" && !last:
				v.Add(tf+".up_to", "only the last tier may be unbounded")
				ok = false
			case t.GetUpTo() == "" && last:
			case last:
				v.Add(tf+".up_to", "the last tier must be unbounded (empty up_to)")
				ok = false
			default:
				units, err := quantity.ParseUnits(t.GetUpTo(), scale)
				if err != nil {
					v.Add(tf+".up_to", quantityMessage(err, scale))
					ok = false
				} else if units <= prev {
					v.Add(tf+".up_to", "tier bounds must be strictly ascending")
					ok = false
				} else {
					prev = units
					tc.UpToUnits = &units
				}
			}
			if err := money.ValidateUnitPrice(t.GetUnitPrice()); err != nil {
				v.Add(tf+".unit_price", err.Error())
				ok = false
			}
			if t.GetFlatFeeMinor() < 0 {
				v.Add(tf+".flat_fee_minor", "must be ≥ 0")
				ok = false
			}
			out.Tiers = append(out.Tiers, tc)
		}
		if !ok {
			return sp, errSkip
		}
		sp.Model, cfg = "TIERED_GRADUATED", out
	case *plansv1.Price_Package:
		units, err := quantity.ParseUnits(m.Package.GetPackageSize(), scale)
		if err != nil || units == 0 {
			msg := "must be > 0"
			if err != nil {
				msg = quantityMessage(err, scale)
			}
			v.Add(field+".package.package_size", msg)
			return sp, errSkip
		}
		if m.Package.GetPackagePriceMinor() < 0 {
			v.Add(field+".package.package_price_minor", "must be ≥ 0")
			return sp, errSkip
		}
		sp.Model, cfg = "PACKAGE", packageConfig{PackageSizeUnits: units, PackagePriceMinor: m.Package.GetPackagePriceMinor()}
	default:
		v.Add(field, "one of per_unit, tiered, package is required")
		return sp, errSkip
	}
	sp.Config, _ = json.Marshal(cfg)
	return sp, nil
}

// PriceFromStored converts a stored price back to the API shape.
func PriceFromStored(meterCode, model string, config json.RawMessage, scale int) (*plansv1.Price, error) {
	p := &plansv1.Price{MeterCode: meterCode}
	switch model {
	case "PER_UNIT":
		var c perUnitConfig
		if err := json.Unmarshal(config, &c); err != nil {
			return nil, err
		}
		p.Model = &plansv1.Price_PerUnit{PerUnit: &plansv1.PerUnitPrice{UnitPrice: c.UnitPrice}}
	case "TIERED_GRADUATED":
		var c tieredConfig
		if err := json.Unmarshal(config, &c); err != nil {
			return nil, err
		}
		tiers := make([]*plansv1.PriceTier, 0, len(c.Tiers))
		for _, t := range c.Tiers {
			pt := &plansv1.PriceTier{UnitPrice: t.UnitPrice, FlatFeeMinor: t.FlatFeeMinor}
			if t.UpToUnits != nil {
				pt.UpTo = quantity.FormatUnits(*t.UpToUnits, scale)
			}
			tiers = append(tiers, pt)
		}
		p.Model = &plansv1.Price_Tiered{Tiered: &plansv1.TieredGraduatedPrice{Tiers: tiers}}
	case "PACKAGE":
		var c packageConfig
		if err := json.Unmarshal(config, &c); err != nil {
			return nil, err
		}
		p.Model = &plansv1.Price_Package{Package: &plansv1.PackagePrice{
			PackageSize: quantity.FormatUnits(c.PackageSizeUnits, scale), PackagePriceMinor: c.PackagePriceMinor}}
	default:
		return nil, fmt.Errorf("unknown price model %q", model)
	}
	return p, nil
}

func quantityMessage(err error, scale int) string {
	switch {
	case errors.Is(err, quantity.ErrPrecision):
		return fmt.Sprintf("at most %d decimal places for this meter", scale)
	case errors.Is(err, quantity.ErrRange):
		return "too large"
	default:
		return "must be a non-negative decimal string such as \"1000\" or \"2.5\""
	}
}

func strictlyAscending(xs []int32) bool {
	for i := 1; i < len(xs); i++ {
		if xs[i] <= xs[i-1] {
			return false
		}
	}
	return true
}

// Fee is a fixed fee in one currency.
type Fee struct {
	Currency     string
	Interval     string // "month" or "year"
	BaseFeeMinor int64
}

// ValidateFees checks the extra fees of PublishPlanVersion: supported currencies, a known interval, non-negative amounts, no
// duplicate (currency, interval). A monthly fee in the tenant's currency is base_fee_minor and cannot be listed; a yearly one can.
func ValidateFees(tenantCurrency string, fees []*plansv1.CurrencyFee) ([]Fee, error) {
	var v Violations
	var out []Fee
	seen := map[string]bool{}
	for i, f := range fees {
		field := fmt.Sprintf("fees[%d]", i)
		if _, ok := money.LookupCurrency(f.GetCurrency()); !ok {
			v.Add(field+".currency", "unsupported currency")
			continue
		}
		interval := "month"
		switch f.GetInterval() {
		case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_UNSPECIFIED, plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH:
		case plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR:
			interval = "year"
		default:
			v.Add(field+".interval", "unknown interval")
			continue
		}
		key := f.GetCurrency() + "|" + interval
		switch {
		case f.GetCurrency() == tenantCurrency && interval == "month":
			v.Add(field+".currency", "the tenant currency's monthly fee is base_fee_minor")
		case seen[key]:
			v.Add(field+".currency", "duplicate currency and interval")
		case f.GetBaseFeeMinor() < 0:
			v.Add(field+".base_fee_minor", "must be ≥ 0")
		default:
			seen[key] = true
			out = append(out, Fee{Currency: f.GetCurrency(), Interval: interval, BaseFeeMinor: f.GetBaseFeeMinor()})
		}
	}
	return out, v.Err()
}

// ValidateCurrencyPrices checks the per-currency usage prices of PublishPlanVersion. Every currency must have a fixed fee
// in fees (the plan is sold in it), and its prices must cover exactly the meters of the base prices. It returns the stored
// form per currency.
func ValidateCurrencyPrices(req *plansv1.PublishPlanVersionRequest, scales map[string]int, fees []Fee) (map[string][]StoredPrice, error) {
	var v Violations
	hasFee := map[string]bool{}
	for _, f := range fees {
		hasFee[f.Currency] = true
	}
	base := map[string]bool{}
	for _, p := range req.GetPrices() {
		base[p.GetMeterCode()] = true
	}
	out := map[string][]StoredPrice{}
	for i, cp := range req.GetCurrencyPrices() {
		field := fmt.Sprintf("currency_prices[%d]", i)
		cur := cp.GetCurrency()
		if !hasFee[cur] {
			v.Add(field+".currency", "must be a currency listed in fees")
			continue
		}
		if _, dup := out[cur]; dup {
			v.Add(field+".currency", "duplicate currency")
			continue
		}
		seen := map[string]bool{}
		var stored []StoredPrice
		for j, p := range cp.GetPrices() {
			pf := fmt.Sprintf("%s.prices[%d]", field, j)
			scale, ok := scales[p.GetMeterCode()]
			switch {
			case !ok:
				v.Add(pf+".meter_code", "unknown meter")
			case !base[p.GetMeterCode()]:
				v.Add(pf+".meter_code", "not priced in the plan version's base prices")
			case seen[p.GetMeterCode()]:
				v.Add(pf+".meter_code", "duplicate meter")
			default:
				seen[p.GetMeterCode()] = true
				if sp, err := priceToStored(p, scale, pf, &v); err == nil {
					stored = append(stored, sp)
				}
			}
		}
		for m := range base {
			if !seen[m] {
				v.Add(field+".prices", "missing a price for meter "+m)
			}
		}
		out[cur] = stored
	}
	return out, v.Err()
}
