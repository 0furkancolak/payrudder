// Package command changes coupons and puts them on, or takes them off, subscriptions.
package command

import (
	"context"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	couponsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/quotify/services/plans-svc/internal/subscriptions/query"
)

var couponCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,40}$`)

var durationToDB = map[plansv1.CouponDuration]string{
	plansv1.CouponDuration_COUPON_DURATION_ONCE:      subscription.CouponOnce,
	plansv1.CouponDuration_COUPON_DURATION_REPEATING: subscription.CouponRepeating,
	plansv1.CouponDuration_COUPON_DURATION_FOREVER:   subscription.CouponForever,
}

// Commands changes coupons.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the coupon commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

// Create adds the coupon described, after checking its code, discount, duration, limit and expiry.
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
		out = couponsqry.Message(sqlc.GetCouponRow(r))
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
	switch {
	case in.GetPercentBps() != 0 && in.GetAmountMinor() != 0:
		v.Add("coupon", "percent_bps or amount_minor, not both")
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
	default:
		v.Add("coupon", "percent_bps or amount_minor is required")
	}
	dur, ok := durationToDB[in.GetDuration()]
	if !ok {
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

// Deactivate stops a coupon from being redeemed again; subscriptions that have it keep it.
func (c *Commands) Deactivate(ctx context.Context, code string) (*plansv1.Coupon, error) {
	var out *plansv1.Coupon
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if n, err := q.DeactivateCoupon(ctx, code); err != nil {
			return err
		} else if n == 0 {
			return status.Error(codes.NotFound, "coupon not found")
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

// Redeem puts a coupon on a subscription, in the caller's transaction: it must be active, unexpired, not used up, and an amount coupon
// must be in the subscription's currency. A subscription a provider renews cannot have one (its fee is the provider's; use a discount at the provider).
func Redeem(ctx context.Context, q *sqlc.Queries, now time.Time, sub sqlc.GetSubscriptionRow, code string) error {
	if sub.Owner == "provider" || sub.FeeTiming != "advance" {
		return status.Error(codes.FailedPrecondition, "a coupon takes something off a fee Payrudder invoices in advance; this subscription's fee is billed another way")
	}
	if _, err := q.GetSubscriptionCoupon(ctx, sub.ID); err == nil {
		return status.Error(codes.FailedPrecondition, "the subscription has a coupon already; remove it first")
	}
	cp, err := q.GetCoupon(ctx, code)
	if err != nil {
		return convert.Err(err, "coupon")
	}
	if cp.AmountMinor != nil && (cp.Currency == nil || *cp.Currency != sub.Currency) {
		return status.Errorf(codes.FailedPrecondition, "coupon %s is an amount in another currency than the subscription's %s", code, sub.Currency)
	}
	if n, err := q.RedeemCoupon(ctx, sqlc.RedeemCouponParams{Code: code, Now: now}); err != nil {
		return err
	} else if n == 0 {
		return status.Errorf(codes.FailedPrecondition, "coupon %s cannot be redeemed: it is inactive, expired or used up", code)
	}
	return q.InsertSubscriptionCoupon(ctx, sqlc.InsertSubscriptionCouponParams{SubscriptionID: sub.ID, CouponCode: code})
}

// Apply puts the coupon on an open-ended subscription and returns the subscription.
func (c *Commands) Apply(ctx context.Context, req *plansv1.ApplyCouponRequest) (*plansv1.Subscription, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if req.GetCode() == "" {
		return nil, domain.Invalid("code", "required")
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if !row.OpenEnded {
			return status.Error(codes.FailedPrecondition, "an ended subscription cannot take a coupon")
		}
		if err := Redeem(ctx, q, c.now(), row, req.GetCode()); err != nil {
			return err
		}
		out = subsqry.Message(row, c.now())
		return subsqry.AttachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}

// Remove takes the coupon off a subscription and returns the subscription.
func (c *Commands) Remove(ctx context.Context, req *plansv1.RemoveCouponRequest) (*plansv1.Subscription, error) {
	id, err := subsqry.ParseID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.GetSubscriptionForUpdate(ctx, id); err != nil {
			return err
		}
		if n, err := q.DeleteSubscriptionCoupon(ctx, id); err != nil {
			return err
		} else if n == 0 {
			return status.Error(codes.FailedPrecondition, "the subscription has no coupon")
		}
		r, err := q.GetSubscription(ctx, id)
		if err != nil {
			return err
		}
		out = subsqry.Message(r, c.now())
		return subsqry.AttachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return out, nil
}
