// Package command changes coupons, promotion codes and redemptions, and puts coupons on, or takes them off, subscriptions.
package command

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/model"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
)

var (
	couponCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,40}$`)
	prefixRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{0,20}$`)
)

const (
	// MaxBatch is the most codes one request makes.
	MaxBatch = 1000
	// randomLen is the length of the random part of a generated code.
	randomLen = 8
	// alphabet leaves out what is mistaken for another character when read aloud or written by hand: 0 O 1 I L.
	alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

var durationToDB = map[plansv1.CouponDuration]string{
	plansv1.CouponDuration_COUPON_DURATION_ONCE:      subscription.CouponOnce,
	plansv1.CouponDuration_COUPON_DURATION_REPEATING: subscription.CouponRepeating,
	plansv1.CouponDuration_COUPON_DURATION_FOREVER:   subscription.CouponForever,
}

// AfterRedeem is told, in the transaction of a checkout, that a customer redeemed a promotion code (its text). The affiliate program uses it to attribute the
// customer to the affiliate the code belongs to.
type AfterRedeem func(ctx context.Context, q *sqlc.Queries, customerID uuid.UUID, promotionCode string, now time.Time) error

// Commands changes coupons.
type Commands struct {
	db       *store.DB
	now      func() time.Time
	onRedeem AfterRedeem
}

// WithRedeemHook sets what happens after a customer redeems a promotion code.
func (c *Commands) WithRedeemHook(h AfterRedeem) *Commands {
	c.onRedeem = h
	return c
}

// New returns the coupon commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

// Create adds the coupon described, after checking its code, discount, duration, limit and expiry. Unless without_default_code is set it also makes the promotion
// code with the coupon's own text, which is how a coupon created before promotion codes existed is used.
func (c *Commands) Create(ctx context.Context, in *plansv1.Coupon) (*plansv1.Coupon, error) {
	p, err := c.parse(in)
	if err != nil {
		return nil, err
	}
	var out *plansv1.Coupon
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		r, err := q.InsertCoupon(ctx, p)
		if err != nil {
			return err
		}
		row := sqlc.GetCouponRow(r)
		if !in.GetWithoutDefaultCode() {
			ids, err := q.InsertPromotionCodes(ctx, sqlc.InsertPromotionCodesParams{CouponID: r.ID, Codes: []string{r.Code}, AllowedPlans: []string{}, AllowedPacks: []string{}, AllowedCurrencies: []string{}})
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				return errkey.New(codes.AlreadyExists, "coupon.code_taken", "a promotion code with this text exists already")
			}
			row.PromotionCodeCount = 1
		}
		out = couponsqry.Message(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return out, nil
}

// parse checks a coupon before it is stored and returns the row to insert.
func (c *Commands) parse(in *plansv1.Coupon) (sqlc.InsertCouponParams, error) {
	var v domain.Violations
	if !couponCodeRe.MatchString(in.GetCode()) {
		v.Add("coupon.code", "2–40 letters, digits, - or _")
	}
	p := sqlc.InsertCouponParams{Code: in.GetCode(), Name: strings.TrimSpace(in.GetName())}
	kinds := 0
	if in.GetPercentBps() != 0 {
		kinds++
	}
	if in.GetAmountMinor() != 0 {
		kinds++
	}
	if in.GetBonusPercentBps() != 0 {
		kinds++
	}
	switch {
	case kinds > 1:
		v.Add("coupon", "one of percent_bps, amount_minor and bonus_percent_bps")
	case in.GetPercentBps() != 0:
		if in.GetPercentBps() < 1 || in.GetPercentBps() > 10000 {
			v.Add("coupon.percent_bps", "1..10000 (1000 is 10 %)")
		}
		pb := in.GetPercentBps()
		p.PercentBps = &pb
	case in.GetAmountMinor() != 0:
		if in.GetAmountMinor() < 0 {
			v.Add("coupon.amount_minor", "must be positive")
		}
		if _, ok := money.LookupCurrency(in.GetCurrency()); !ok {
			v.Add("coupon.currency", "an amount coupon needs a supported currency")
		}
		am, cur := in.GetAmountMinor(), in.GetCurrency()
		p.AmountMinor, p.Currency = &am, &cur
	case in.GetBonusPercentBps() != 0:
		if in.GetBonusPercentBps() < 1 || in.GetBonusPercentBps() > 100000 {
			v.Add("coupon.bonus_percent_bps", "1..100000 (1000 is 10 % more credits)")
		}
		bb := in.GetBonusPercentBps()
		p.BonusPercentBps = &bb
	default:
		v.Add("coupon", "percent_bps, amount_minor or bonus_percent_bps is required")
	}
	if in.GetCurrency() != "" && in.GetAmountMinor() == 0 {
		v.Add("coupon.currency", "only an amount coupon has a currency")
	}
	p.AppliesTo = appliesTo(in, &v)
	forSub := slices.Contains(p.AppliesTo, model.TargetSubscription)
	dur, ok := durationToDB[in.GetDuration()]
	switch {
	case ok:
	case !forSub && in.GetDuration() == plansv1.CouponDuration_COUPON_DURATION_UNSPECIFIED:
		dur = subscription.CouponOnce // a duration is how long a subscription keeps the coupon; other targets are one purchase
	default:
		v.Add("coupon.duration", "once, repeating or forever")
	}
	p.Duration = dur
	if in.GetDuration() == plansv1.CouponDuration_COUPON_DURATION_REPEATING {
		if in.GetDurationPeriods() < 1 {
			v.Add("coupon.duration_periods", "repeating needs at least 1 period")
		}
		dp := in.GetDurationPeriods()
		p.DurationPeriods = &dp
	} else if in.GetDurationPeriods() != 0 {
		v.Add("coupon.duration_periods", "only for a repeating coupon")
	}
	if in.GetMaxRedemptions() < 0 {
		v.Add("coupon.max_redemptions", "must not be negative")
	} else if in.GetMaxRedemptions() > 0 {
		mr := in.GetMaxRedemptions()
		p.MaxRedemptions = &mr
	}
	if in.GetExpireTime() != nil {
		if err := in.GetExpireTime().CheckValid(); err != nil || !in.GetExpireTime().AsTime().After(c.now()) {
			v.Add("coupon.expire_time", "must be a time in the future")
		} else {
			e := in.GetExpireTime().AsTime()
			p.ExpiresAt = &e
		}
	}
	return p, v.Err()
}

// appliesTo returns where the coupon works: what was asked, or everything (a bonus coupon: credit purchases).
func appliesTo(in *plansv1.Coupon, v *domain.Violations) []string {
	var out []string
	for _, t := range in.GetAppliesTo() {
		name, ok := couponsqry.TargetToDB[t]
		if !ok {
			v.Add("coupon.applies_to", "subscription, credit purchase or payment")
			continue
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	if in.GetBonusPercentBps() != 0 {
		if len(out) > 1 || (len(out) == 1 && out[0] != model.TargetCreditPurchase) {
			v.Add("coupon.applies_to", "bonus credits are for a credit purchase only")
		}
		return []string{model.TargetCreditPurchase}
	}
	if len(out) == 0 {
		return slices.Clone(model.AllTargets)
	}
	return out
}

// Deactivate stops a coupon from being redeemed again; subscriptions that have it keep it.
func (c *Commands) Deactivate(ctx context.Context, code string) (*plansv1.Coupon, error) {
	var out *plansv1.Coupon
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if n, err := q.DeactivateCoupon(ctx, code); err != nil {
			return err
		} else if n == 0 {
			return errkey.New(codes.NotFound, "coupon.not_found", "coupon not found")
		}
		r, err := q.GetCoupon(ctx, code)
		if err != nil {
			return err
		}
		out = couponsqry.Message(r)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return out, nil
}

// Redeem puts a coupon on a subscription, in the caller's transaction. The code is a promotion code (with its limits and restrictions) or, as an operator may
// apply it, the coupon's own code. It must be active, unexpired, not used up, and an amount coupon must be in the subscription's currency. A subscription a
// provider renews cannot have one (its fee is the provider's; use a discount at the provider).
func Redeem(ctx context.Context, q *sqlc.Queries, now time.Time, sub sqlc.GetSubscriptionRow, code string) error {
	if sub.Owner == domain.OwnerProvider || sub.FeeTiming != subscription.FeeInAdvance {
		return errkey.New(codes.FailedPrecondition, "plans.coupon.fee_not_in_advance", "a coupon takes something off a fee Payrudder invoices in advance; this subscription's fee is billed another way")
	}
	if _, err := q.GetSubscriptionCoupon(ctx, sub.ID); err == nil {
		return errkey.New(codes.FailedPrecondition, "coupon.already_applied", "the subscription has a coupon already; remove it first")
	}
	others, err := q.CountOtherSubscriptions(ctx, sqlc.CountOtherSubscriptionsParams{CustomerID: sub.CustomerID, ExceptID: sub.ID})
	if err != nil {
		return err
	}
	got, err := Claim(ctx, q, ClaimInput{Code: code, ByCoupon: true, CustomerID: sub.CustomerID, Target: model.TargetSubscription, TargetID: sub.ID, Currency: sub.Currency, Item: sub.PlanCode,
		PriorPurchases: others > 0, Now: now})
	if err != nil {
		return err
	}
	if !got.Applied {
		return errkey.New(codes.NotFound, model.KeyNotFound, "no such code")
	}
	return q.InsertSubscriptionCoupon(ctx, sqlc.InsertSubscriptionCouponParams{SubscriptionID: sub.ID, CouponCode: got.CouponCode, RedemptionID: &got.RedemptionID})
}

// Apply puts the coupon on an open-ended subscription and returns the subscription.
func (c *Commands) Apply(ctx context.Context, req *plansv1.ApplyCouponRequest) (*plansv1.Subscription, error) {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if req.GetCode() == "" {
		return nil, domain.Invalid("code", "required")
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := subsqry.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		if !row.OpenEnded {
			return errkey.New(codes.FailedPrecondition, "plans.coupon.subscription_ended", "an ended subscription cannot take a coupon")
		}
		if err := Redeem(ctx, q, c.now(), row, req.GetCode()); err != nil {
			return err
		}
		out = convert.SubscriptionOf(row, c.now())
		return subsqry.AttachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// Remove takes the coupon off a subscription and returns the subscription. The redemption stays in the ledger and in the count.
func (c *Commands) Remove(ctx context.Context, req *plansv1.RemoveCouponRequest) (*plansv1.Subscription, error) {
	id, err := domain.ParseID("subscription_id", req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetSubscriptionForUpdate(ctx, id); err != nil {
			return err
		}
		if rid, err := q.GetSubscriptionRedemptionID(ctx, id); err == nil && rid != nil {
			// The purchase no longer uses the coupon, so it may take another; the place it took in the count stays taken unless its code gives it back.
			if row, err := q.LockRedemptionByID(ctx, *rid); err == nil {
				if err := reverseLocked(ctx, q, row, c.now()); err != nil {
					return err
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if n, err := q.DeleteSubscriptionCoupon(ctx, id); err != nil {
			return err
		} else if n == 0 {
			return errkey.New(codes.FailedPrecondition, "plans.coupon.none_on_subscription", "the subscription has no coupon")
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = convert.SubscriptionOf(r, c.now())
		return subsqry.AttachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// CreatePromotionCodes makes the codes of a coupon: the ones named, or count random ones.
func (c *Commands) CreatePromotionCodes(ctx context.Context, req *plansv1.CreatePromotionCodesRequest) ([]*plansv1.PromotionCode, error) {
	p, err := c.parseCodes(req)
	if err != nil {
		return nil, err
	}
	var out []*plansv1.PromotionCode
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cp, err := q.GetCoupon(ctx, req.GetCouponCode())
		if err != nil {
			return err
		}
		p.CouponID = cp.ID
		ids, err := insertCodes(ctx, q, p, req)
		if err != nil {
			return err
		}
		rows, err := q.ListPromotionCodesByID(ctx, ids)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, couponsqry.PromotionMessage(couponsqry.PromotionCodeRow(r)))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return out, nil
}

func insertCodes(ctx context.Context, q *sqlc.Queries, p sqlc.InsertPromotionCodesParams, req *plansv1.CreatePromotionCodesRequest) ([]uuid.UUID, error) {
	if len(req.GetCodes()) > 0 {
		p.Codes = req.GetCodes()
		ids, err := q.InsertPromotionCodes(ctx, p)
		if err != nil {
			return nil, err
		}
		if len(ids) != len(p.Codes) {
			return nil, errkey.New(codes.AlreadyExists, "coupon.code_taken", "one of these codes exists already (a code is the same in any case)")
		}
		return ids, nil
	}
	var all []uuid.UUID
	for round := 0; len(all) < int(req.GetCount()) && round < 5; round++ {
		batch := make([]string, 0, int(req.GetCount())-len(all))
		for len(batch) < cap(batch) {
			s, err := randomCode(req.GetPrefix())
			if err != nil {
				return nil, status.Error(codes.Internal, "internal error")
			}
			batch = append(batch, s)
		}
		p.Codes = batch
		ids, err := q.InsertPromotionCodes(ctx, p) // a random code that exists already is skipped; the next round makes up for it
		if err != nil {
			return nil, err
		}
		all = append(all, ids...)
	}
	if len(all) < int(req.GetCount()) {
		return nil, errkey.New(codes.Unavailable, "plans.coupon.codes_exhausted", "could not find enough unused codes; try a longer prefix")
	}
	return all, nil
}

func randomCode(prefix string) (string, error) {
	var b strings.Builder
	b.WriteString(prefix)
	n := big.NewInt(int64(len(alphabet)))
	for range randomLen {
		i, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[i.Int64()])
	}
	return b.String(), nil
}

// parseCodes checks the request and returns the parameters the codes share.
func (c *Commands) parseCodes(req *plansv1.CreatePromotionCodesRequest) (sqlc.InsertPromotionCodesParams, error) {
	var v domain.Violations
	var p sqlc.InsertPromotionCodesParams
	t := req.GetTemplate()
	if req.GetCouponCode() == "" {
		v.Add("coupon_code", "required")
	}
	switch {
	case len(req.GetCodes()) > 0 && req.GetCount() != 0:
		v.Add("count", "codes or count, not both")
	case len(req.GetCodes()) > MaxBatch:
		v.Add("codes", "at most 1000 codes in one request")
	case len(req.GetCodes()) > 0:
		seen := map[string]bool{}
		for _, code := range req.GetCodes() {
			if !couponCodeRe.MatchString(code) {
				v.Add("codes", code+": 2–40 letters, digits, - or _")
			}
			if seen[strings.ToUpper(code)] {
				v.Add("codes", code+": listed twice")
			}
			seen[strings.ToUpper(code)] = true
		}
	case req.GetCount() < 1 || req.GetCount() > MaxBatch:
		v.Add("count", "1 to 1000 random codes, or name the codes")
	}
	if !prefixRe.MatchString(req.GetPrefix()) {
		v.Add("prefix", "at most 20 letters, digits, - or _")
	}
	if t.GetMaxRedemptions() < 0 {
		v.Add("template.max_redemptions", "must not be negative")
	} else if t.GetMaxRedemptions() > 0 {
		mr := t.GetMaxRedemptions()
		p.MaxRedemptions = &mr
	}
	if t.GetExpireTime() != nil {
		if err := t.GetExpireTime().CheckValid(); err != nil || !t.GetExpireTime().AsTime().After(c.now()) {
			v.Add("template.expire_time", "must be a time in the future")
		} else {
			e := t.GetExpireTime().AsTime()
			p.ExpiresAt = &e
		}
	}
	if t.GetMinAmountMinor() < 0 {
		v.Add("template.min_amount_minor", "must not be negative")
	} else if t.GetMinAmountMinor() > 0 {
		if _, ok := money.LookupCurrency(t.GetMinAmountCurrency()); !ok {
			v.Add("template.min_amount_currency", "a minimum amount needs a supported currency")
		}
		ma, cur := t.GetMinAmountMinor(), t.GetMinAmountCurrency()
		p.MinAmountMinor, p.MinAmountCurrency = &ma, &cur
	} else if t.GetMinAmountCurrency() != "" {
		v.Add("template.min_amount_currency", "only with min_amount_minor")
	}
	for _, cur := range t.GetAllowedCurrencies() {
		if _, ok := money.LookupCurrency(cur); !ok {
			v.Add("template.allowed_currencies", cur+": not a supported currency")
		}
	}
	if len(t.GetAllowedPlans()) > 100 || len(t.GetAllowedPacks()) > 100 {
		v.Add("template", "at most 100 allowed plans and 100 allowed packs")
	}
	p.FirstPurchaseOnly, p.OnePerCustomer, p.RestoreOnReversal = t.GetFirstPurchaseOnly(), t.GetOnePerCustomer(), t.GetRestoreOnReversal()
	p.AllowedPlans, p.AllowedPacks, p.AllowedCurrencies = nonNil(t.GetAllowedPlans()), nonNil(t.GetAllowedPacks()), nonNil(t.GetAllowedCurrencies())
	return p, v.Err()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DeactivatePromotionCode stops one code; the coupon and its other codes keep working.
func (c *Commands) DeactivatePromotionCode(ctx context.Context, code string) (*plansv1.PromotionCode, error) {
	var out *plansv1.PromotionCode
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if n, err := q.DeactivatePromotionCode(ctx, code); err != nil {
			return err
		} else if n == 0 {
			return errkey.New(codes.NotFound, model.KeyNotFound, "no such code")
		}
		r, err := q.GetPromotionCodeByText(ctx, code)
		if err != nil {
			return err
		}
		out = couponsqry.PromotionMessage(r)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "promotion code")
	}
	return out, nil
}

// RedeemPromotion is the request of a checkout: check a typed code (or the coupon waiting for the customer) against a purchase and claim it.
func (c *Commands) RedeemPromotion(ctx context.Context, req *plansv1.RedeemPromotionRequest) (*plansv1.RedeemPromotionResponse, error) {
	in, err := c.claimInput(req)
	if err != nil {
		return nil, err
	}
	var got Claimed
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if ext := req.GetCustomerExternalId(); ext != "" {
			cu, err := q.GetCustomerByExternalID(ctx, ext)
			if err != nil {
				return err
			}
			in.CustomerID = cu.ID
		}
		var err error
		got, err = Claim(ctx, q, in)
		if err != nil {
			return err
		}
		if c.onRedeem != nil && !in.Preview && got.Applied && got.PromotionCode != "" && in.CustomerID != uuid.Nil {
			return c.onRedeem(ctx, q, in.CustomerID, got.PromotionCode, in.Now)
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	out := &plansv1.RedeemPromotionResponse{Applied: got.Applied, CouponCode: got.CouponCode, CouponName: got.CouponName, PromotionCode: got.PromotionCode,
		DiscountMinor: got.DiscountMinor, BonusCredits: got.BonusCredits, TotalMinor: req.GetAmountMinor() - got.DiscountMinor, Currency: req.GetCurrency()}
	if got.RedemptionID != uuid.Nil {
		out.RedemptionId = got.RedemptionID.String()
	}
	return out, nil
}

func (c *Commands) claimInput(req *plansv1.RedeemPromotionRequest) (ClaimInput, error) {
	var v domain.Violations
	target, ok := couponsqry.TargetToDB[req.GetTarget()]
	if !ok || target == model.TargetSubscription {
		v.Add("target", "credit purchase or payment (a subscription takes its coupon when it is created)")
	}
	in := ClaimInput{Code: strings.TrimSpace(req.GetCode()), Target: target, AmountMinor: req.GetAmountMinor(), Currency: req.GetCurrency(), Item: req.GetItem(), Credits: req.GetCredits(),
		PriorPurchases: req.GetPriorPurchases(), Preview: req.GetPreview(), Now: c.now()}
	id, err := uuid.Parse(req.GetTargetId())
	if err != nil && !req.GetPreview() {
		v.Add("target_id", "the id of the purchase's invoice")
	}
	in.TargetID = id
	if req.GetInvoiceId() != "" {
		inv, err := uuid.Parse(req.GetInvoiceId())
		if err != nil {
			v.Add("invoice_id", "not a UUID")
		}
		in.InvoiceID = &inv
	}
	if req.GetAmountMinor() < 0 {
		v.Add("amount_minor", "must not be negative")
	}
	if _, ok := money.LookupCurrency(req.GetCurrency()); !ok {
		v.Add("currency", "not a supported currency")
	}
	if req.GetCredits() < 0 {
		v.Add("credits", "must not be negative")
	}
	return in, v.Err()
}

// ReversePromotion gives back the redemption of a purchase that was voided or fully refunded.
func (c *Commands) ReversePromotion(ctx context.Context, req *plansv1.ReversePromotionRequest) (bool, error) {
	target, ok := couponsqry.TargetToDB[req.GetTarget()]
	id, err := uuid.Parse(req.GetTargetId())
	if !ok || err != nil {
		return false, domain.Invalid("target_id", "a target and the id of the purchase")
	}
	var reversed bool
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		reversed, err = Reverse(ctx, q, target, id, c.now())
		return err
	})
	if err != nil {
		return false, convert.Err(err, "coupon")
	}
	return reversed, nil
}

// GrantCustomerCoupon puts a coupon in wait for a customer: the customer's next eligible checkout without a typed code uses it. The same (customer, source, ref)
// grants once.
func (c *Commands) GrantCustomerCoupon(ctx context.Context, req *plansv1.GrantCustomerCouponRequest) (bool, error) {
	var v domain.Violations
	if req.GetCustomerExternalId() == "" {
		v.Add("customer_external_id", "required")
	}
	if req.GetCouponCode() == "" {
		v.Add("coupon_code", "required")
	}
	if src := req.GetSource(); src == "" || len(src) > 40 {
		v.Add("source", "1 to 40 characters: who gives the coupon")
	}
	if ref := req.GetSourceRef(); ref == "" || len(ref) > 200 {
		v.Add("source_ref", "1 to 200 characters: the reference that makes this grant one")
	}
	if err := v.Err(); err != nil {
		return false, err
	}
	var granted bool
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cu, err := q.GetCustomerByExternalID(ctx, req.GetCustomerExternalId())
		if err != nil {
			return err
		}
		cp, err := q.GetCoupon(ctx, req.GetCouponCode())
		if errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.NotFound, model.KeyNotFound, "no such coupon")
		}
		if err != nil {
			return err
		}
		granted, err = GrantToCustomer(ctx, q, cu.ID, cp.ID, req.GetSource(), req.GetSourceRef())
		return err
	})
	if err != nil {
		return false, convert.Err(err, "customer")
	}
	return granted, nil
}

// GrantToCustomer puts a coupon in wait for a customer in the caller's transaction: the customer's next eligible checkout without a typed code uses it. The same
// (customer, source, ref) grants once; it reports whether this call was the one that did. The affiliate program uses it for the customers it brings.
func GrantToCustomer(ctx context.Context, q *sqlc.Queries, customerID, couponID uuid.UUID, source, ref string) (bool, error) {
	n, err := q.InsertCustomerCoupon(ctx, sqlc.InsertCustomerCouponParams{CustomerID: customerID, CouponID: couponID, Source: source, SourceRef: ref})
	return n == 1, err
}
