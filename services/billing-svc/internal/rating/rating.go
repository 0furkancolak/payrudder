// Package rating prices usage. It is pure: no I/O, no clock, no floats (ADR 0010).
//
// Unit prices are decimals in minor units of the tenant's currency ("0.002" is a fifth of a kuruş per token). A line's
// amount is computed at full precision and rounded half-even to an integer minor unit once, at line level. An invoice
// total is the sum of its rounded lines.
package rating

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/cockroachdb/apd/v3"
)

// Model is a pricing model.
type Model int

const (
	PerUnit Model = iota
	TieredGraduated
	Package
)

// Tier is one band of a graduated price.
type Tier struct {
	// UpTo is the inclusive upper bound in meter units; nil means unbounded (the last tier only).
	UpTo         *apd.Decimal
	UnitPrice    *apd.Decimal
	FlatFeeMinor int64
}

// Price turns a quantity of one meter into money.
type Price struct {
	MeterCode string
	Model     Model
	// PerUnit.
	UnitPrice *apd.Decimal
	// TieredGraduated.
	Tiers []Tier
	// Package: ceil(qty / PackageSize) × PackagePriceMinor.
	PackageSize       *apd.Decimal
	PackagePriceMinor int64
}

// PlanVersion is the part of a plan version rating needs.
type PlanVersion struct {
	ID           string
	PlanCode     string
	BaseFeeMinor int64
	Prices       map[string]Price
}

// Usage is the quantity of one meter, as an exact decimal in meter units.
type Usage struct {
	MeterCode string
	Quantity  *apd.Decimal
	UnitLabel string
}

// Kind of an invoice line.
type Kind string

const (
	KindBaseFee   Kind = "BASE_FEE"
	KindUsage     Kind = "USAGE"
	KindLateUsage Kind = "LATE_USAGE"
	// KindCorrection is a line of a CORRECTION invoice: the difference one component of a closed period would have cost.
	KindCorrection Kind = "CORRECTION"
	// KindCredit is a negative line that applies a credit balance to a regular invoice.
	KindCredit Kind = "CREDIT"
	// KindDiscount is a negative line: what a coupon takes off.
	KindDiscount Kind = "DISCOUNT"
	// KindTax is a line of tax at a configured rate.
	KindTax Kind = "TAX"
)

// Line is one invoice line.
type Line struct {
	Kind         Kind
	MeterCode    string
	OriginPeriod string // LATE_USAGE only
	Quantity     *apd.Decimal
	UnitLabel    string
	UnitPrice    string // display, PER_UNIT only
	AmountMinor  int64
	Description  string
	// CouponCode names the coupon that made a DISCOUNT line (empty for any other line).
	CouponCode string
	// ServiceFrom and ServiceTo are the service period the line covers; zero when it does not say.
	ServiceFrom, ServiceTo time.Time
}

// ErrOverflow means an amount does not fit an int64 number of minor units.
var ErrOverflow = errors.New("rating: amount overflows int64 minor units")

var ctx = apd.Context{
	Precision:   120,
	MaxExponent: apd.MaxExponent,
	MinExponent: apd.MinExponent,
	Traps:       apd.DefaultTraps,
	Rounding:    apd.RoundHalfEven,
}

func zero() *apd.Decimal { return apd.New(0, 0) }

// exact computes the price of qty at full precision.
func exact(p Price, qty *apd.Decimal) (*apd.Decimal, error) {
	if qty.Sign() < 0 {
		return nil, fmt.Errorf("rating: negative quantity %s", qty.String())
	}
	switch p.Model {
	case PerUnit:
		var out apd.Decimal
		if _, err := ctx.Mul(&out, qty, p.UnitPrice); err != nil {
			return nil, err
		}
		return &out, nil
	case TieredGraduated:
		total := zero()
		prev := zero()
		for _, t := range p.Tiers {
			upper := qty
			if t.UpTo != nil && t.UpTo.Cmp(qty) < 0 {
				upper = t.UpTo
			}
			if upper.Cmp(prev) > 0 {
				var inTier, amount apd.Decimal
				if _, err := ctx.Sub(&inTier, upper, prev); err != nil {
					return nil, err
				}
				if _, err := ctx.Mul(&amount, &inTier, t.UnitPrice); err != nil {
					return nil, err
				}
				if _, err := ctx.Add(total, total, &amount); err != nil {
					return nil, err
				}
				if t.FlatFeeMinor != 0 {
					if _, err := ctx.Add(total, total, apd.New(t.FlatFeeMinor, 0)); err != nil {
						return nil, err
					}
				}
			}
			if t.UpTo == nil || t.UpTo.Cmp(qty) >= 0 {
				break
			}
			prev = t.UpTo
		}
		return total, nil
	case Package:
		if p.PackageSize == nil || p.PackageSize.Sign() <= 0 {
			return nil, errors.New("rating: package size must be positive")
		}
		var packs apd.Decimal
		if _, err := ctx.Quo(&packs, qty, p.PackageSize); err != nil {
			return nil, err
		}
		if _, err := ctx.Ceil(&packs, &packs); err != nil {
			return nil, err
		}
		var out apd.Decimal
		if _, err := ctx.Mul(&out, &packs, apd.New(p.PackagePriceMinor, 0)); err != nil {
			return nil, err
		}
		return &out, nil
	}
	return nil, fmt.Errorf("rating: unknown model %d", p.Model)
}

// Amount is the price of qty in whole minor units, rounded half-even once.
func Amount(p Price, qty *apd.Decimal) (int64, error) {
	e, err := exact(p, qty)
	if err != nil {
		return 0, err
	}
	var r apd.Decimal
	if _, err := ctx.Quantize(&r, e, 0); err != nil {
		return 0, err
	}
	v, err := r.Int64()
	if err != nil {
		return 0, ErrOverflow
	}
	return v, nil
}

// Incremental is what a late quantity adds to the price of a period: Amount(base+late) − Amount(base). Tiers make
// it differ from pricing the late quantity alone.
func Incremental(p Price, base, late *apd.Decimal) (int64, error) {
	var sum apd.Decimal
	if _, err := ctx.Add(&sum, base, late); err != nil {
		return 0, err
	}
	hi, err := Amount(p, &sum)
	if err != nil {
		return 0, err
	}
	lo, err := Amount(p, base)
	if err != nil {
		return 0, err
	}
	return hi - lo, nil
}

// Rate prices a customer's usage of one period under a plan version: the base fee, then one line per used meter that
// the plan prices, ordered by meter code. Usage of a meter the plan does not price is free and yields no line.
func Rate(pv PlanVersion, usage []Usage) ([]Line, error) {
	var lines []Line
	if pv.BaseFeeMinor != 0 {
		lines = append(lines, Line{Kind: KindBaseFee, AmountMinor: pv.BaseFeeMinor, Description: "Base fee"})
	}
	used, err := RateUsage(pv, usage)
	if err != nil {
		return nil, err
	}
	return append(lines, used...), nil
}

// RateUsage prices only the metered part of Rate: one line per used meter that the plan prices, ordered by meter code.
func RateUsage(pv PlanVersion, usage []Usage) ([]Line, error) {
	var lines []Line
	byMeter := make([]Usage, len(usage))
	copy(byMeter, usage)
	sortUsage(byMeter)
	for _, u := range byMeter {
		p, ok := pv.Prices[u.MeterCode]
		if !ok || u.Quantity.Sign() == 0 {
			continue
		}
		amt, err := Amount(p, u.Quantity)
		if err != nil {
			return nil, fmt.Errorf("meter %s: %w", u.MeterCode, err)
		}
		l := Line{Kind: KindUsage, MeterCode: u.MeterCode, Quantity: u.Quantity, UnitLabel: u.UnitLabel, AmountMinor: amt,
			Description: fmt.Sprintf("Usage — %s — %s %s", u.MeterCode, u.Quantity.Text('f'), u.UnitLabel)}
		if p.Model == PerUnit {
			l.UnitPrice = p.UnitPrice.Text('f')
		}
		lines = append(lines, l)
	}
	return lines, nil
}

func sortUsage(u []Usage) {
	for i := 1; i < len(u); i++ {
		for j := i; j > 0 && u[j].MeterCode < u[j-1].MeterCode; j-- {
			u[j], u[j-1] = u[j-1], u[j]
		}
	}
}

// LateLine prices late quantity `late` of an origin period incrementally on top of what that period (and earlier late
// batches) already billed (`base`), under the origin period's plan version.
func LateLine(pv PlanVersion, meter, unitLabel, origin string, base, late *apd.Decimal) (Line, bool, error) {
	p, ok := pv.Prices[meter]
	if !ok || late.Sign() == 0 {
		return Line{}, false, nil
	}
	amt, err := Incremental(p, base, late)
	if err != nil {
		return Line{}, false, fmt.Errorf("meter %s: %w", meter, err)
	}
	return Line{Kind: KindLateUsage, MeterCode: meter, OriginPeriod: origin, Quantity: late, UnitLabel: unitLabel, AmountMinor: amt,
		Description: fmt.Sprintf("Late usage — %s — %s (%s %s)", meter, origin, late.Text('f'), unitLabel)}, true, nil
}

// Total is the sum of the lines' amounts, or ErrOverflow.
func Total(lines []Line) (int64, error) {
	var total int64
	for _, l := range lines {
		if (l.AmountMinor > 0 && total > math.MaxInt64-l.AmountMinor) || (l.AmountMinor < 0 && total < math.MinInt64-l.AmountMinor) {
			return 0, ErrOverflow
		}
		total += l.AmountMinor
	}
	return total, nil
}
