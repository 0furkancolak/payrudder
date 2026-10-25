// Package model holds the rules of a coupon and its promotion code as pure functions: what a purchase may use, and what it takes off. No database here, so the
// rules are tested by table.
package model

import (
	"slices"
	"time"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/subscription"
)

// Targets a coupon can be used on.
const (
	TargetSubscription   = "subscription"
	TargetCreditPurchase = "credit_purchase"
	TargetPayment        = "payment"
	// TargetLegacy marks the redemptions of coupons that existed before the ledger and cannot be traced to a purchase.
	TargetLegacy = "legacy"
)

// AllTargets is where a coupon works unless it says otherwise.
var AllTargets = []string{TargetSubscription, TargetCreditPurchase, TargetPayment}

// Error keys of a refused coupon (docs/error-keys.md).
var (
	KeyNotFound         = errkey.Key("coupon.not_found")
	KeyInactive         = errkey.Key("coupon.inactive")
	KeyExpired          = errkey.Key("coupon.expired")
	KeyExhausted        = errkey.Key("coupon.exhausted")
	KeyNotApplicable    = errkey.Key("coupon.not_applicable")
	KeyCurrencyMismatch = errkey.Key("coupon.currency_mismatch")
	KeyAlreadyUsed      = errkey.Key("coupon.already_used")
	KeyFirstPurchase    = errkey.Key("coupon.first_purchase_only")
	KeyBelowMinimum     = errkey.Key("coupon.below_minimum")
	KeyCustomerRequired = errkey.Key("coupon.customer_required")
	KeyAlreadyApplied   = errkey.Key("coupon.already_applied")
)

// Rejection is why a coupon cannot be used: a key for the client and an English message.
type Rejection struct {
	Key string
	Msg string
}

func (r *Rejection) Error() string { return r.Key + ": " + r.Msg }

func reject(key, msg string) *Rejection { return &Rejection{Key: key, Msg: msg} }

// Coupon is the discount definition.
type Coupon struct {
	Active      bool
	ExpiresAt   *time.Time
	MaxRedeemed *int32
	Redeemed    int32
	// Exactly one of Percent, Amount and BonusBPS is set.
	PercentBPS  int32
	AmountMinor int64
	Currency    string
	BonusBPS    int32
	AppliesTo   []string
}

// Code is a promotion code's limits and restrictions; the zero value restricts nothing. A nil *Code means the coupon is used directly (an operator applies it
// by its own code, or a customer's waiting coupon).
type Code struct {
	Active            bool
	ExpiresAt         *time.Time
	MaxRedeemed       *int32
	Redeemed          int32
	FirstPurchaseOnly bool
	OnePerCustomer    bool
	MinAmountMinor    int64
	MinAmountCurrency string
	AllowedPlans      []string
	AllowedPacks      []string
	AllowedCurrencies []string
}

// Purchase is what the coupon is asked to be used on.
type Purchase struct {
	Target      string
	AmountMinor int64
	Currency    string
	// Item is the plan code or the credit pack id.
	Item    string
	Credits int64
	// CustomerKnown is false for a guest payment link.
	CustomerKnown  bool
	PriorPurchases bool
	// CodeUses is how often this customer has used this code.
	CodeUses int32
	Now      time.Time
}

// Result is what the coupon takes off the purchase.
type Result struct {
	DiscountMinor int64
	BonusCredits  int64
}

// Evaluate decides whether the purchase may use the coupon (through the code, if any) and what it takes off. It never takes the total below zero. The count
// checks are advisory here: the claim in the database is what decides when two checkouts race for the last redemption.
func Evaluate(c Coupon, code *Code, p Purchase) (Result, *Rejection) {
	if !c.Active || (code != nil && !code.Active) {
		return Result{}, reject(KeyInactive, "this code is no longer active")
	}
	if (c.ExpiresAt != nil && !c.ExpiresAt.After(p.Now)) || (code != nil && code.ExpiresAt != nil && !code.ExpiresAt.After(p.Now)) {
		return Result{}, reject(KeyExpired, "this code has expired")
	}
	if (c.MaxRedeemed != nil && c.Redeemed >= *c.MaxRedeemed) || (code != nil && code.MaxRedeemed != nil && code.Redeemed >= *code.MaxRedeemed) {
		return Result{}, reject(KeyExhausted, "this code has been used up")
	}
	targets := c.AppliesTo
	if len(targets) == 0 {
		targets = AllTargets
	}
	if !slices.Contains(targets, p.Target) {
		return Result{}, reject(KeyNotApplicable, "this code does not work on this kind of purchase")
	}
	if code != nil {
		if r := checkCode(*code, p); r != nil {
			return Result{}, r
		}
	}
	if c.AmountMinor > 0 && c.Currency != p.Currency {
		return Result{}, reject(KeyCurrencyMismatch, "this code is an amount in "+c.Currency+", which is not the currency of the purchase")
	}
	return compute(c, p)
}

func compute(c Coupon, p Purchase) (Result, *Rejection) {
	if p.Target == TargetSubscription {
		return Result{}, nil // a fee is taken off period by period, when it is invoiced
	}
	var r Result
	switch {
	case c.PercentBPS > 0:
		r.DiscountMinor = min(subscription.ScaleHalfEven(p.AmountMinor, int64(c.PercentBPS), 10000), p.AmountMinor)
	case c.AmountMinor > 0:
		r.DiscountMinor = min(c.AmountMinor, p.AmountMinor)
	case c.BonusBPS > 0:
		if p.Target != TargetCreditPurchase {
			return Result{}, reject(KeyNotApplicable, "bonus credits belong to a credit purchase")
		}
		r.BonusCredits = p.Credits * int64(c.BonusBPS) / 10000
	}
	if r.DiscountMinor <= 0 && r.BonusCredits <= 0 {
		return Result{}, reject(KeyNotApplicable, "this code takes nothing off this purchase")
	}
	return r, nil
}

func checkCode(code Code, p Purchase) *Rejection {
	if len(code.AllowedCurrencies) > 0 && !slices.Contains(code.AllowedCurrencies, p.Currency) {
		return reject(KeyCurrencyMismatch, "this code does not work in "+p.Currency)
	}
	switch {
	case p.Target == TargetSubscription && len(code.AllowedPlans) > 0 && !slices.Contains(code.AllowedPlans, p.Item):
		return reject(KeyNotApplicable, "this code does not work on this plan")
	case p.Target == TargetCreditPurchase && len(code.AllowedPacks) > 0 && !slices.Contains(code.AllowedPacks, p.Item):
		return reject(KeyNotApplicable, "this code does not work on this credit pack")
	case p.Target == TargetPayment && (len(code.AllowedPlans) > 0 || len(code.AllowedPacks) > 0):
		return reject(KeyNotApplicable, "this code is for a plan or a credit pack, not for a payment")
	}
	if code.MinAmountMinor > 0 && p.Target != TargetSubscription {
		if code.MinAmountCurrency != p.Currency {
			return reject(KeyCurrencyMismatch, "the minimum of this code is in "+code.MinAmountCurrency)
		}
		if p.AmountMinor < code.MinAmountMinor {
			return reject(KeyBelowMinimum, "the purchase is below the minimum of this code")
		}
	}
	if (code.FirstPurchaseOnly || code.OnePerCustomer) && !p.CustomerKnown {
		return reject(KeyCustomerRequired, "this code needs to know the customer")
	}
	if code.FirstPurchaseOnly && p.PriorPurchases {
		return reject(KeyFirstPurchase, "this code is for a first purchase only")
	}
	if code.OnePerCustomer && p.CodeUses > 0 {
		return reject(KeyAlreadyUsed, "this customer has used this code already")
	}
	return nil
}
