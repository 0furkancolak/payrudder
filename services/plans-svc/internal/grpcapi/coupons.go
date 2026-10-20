package grpcapi

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

var couponCodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,40}$`)

var (
	durationToDB = map[plansv1.CouponDuration]string{
		plansv1.CouponDuration_COUPON_DURATION_ONCE:      subscription.CouponOnce,
		plansv1.CouponDuration_COUPON_DURATION_REPEATING: subscription.CouponRepeating,
		plansv1.CouponDuration_COUPON_DURATION_FOREVER:   subscription.CouponForever,
	}
	durationFromDB = map[string]plansv1.CouponDuration{
		subscription.CouponOnce:      plansv1.CouponDuration_COUPON_DURATION_ONCE,
		subscription.CouponRepeating: plansv1.CouponDuration_COUPON_DURATION_REPEATING,
		subscription.CouponForever:   plansv1.CouponDuration_COUPON_DURATION_FOREVER,
	}
)

func couponMessage(r sqlc.GetCouponRow) *plansv1.Coupon {
	c := &plansv1.Coupon{
		Code: r.Code, Name: r.Name, Duration: durationFromDB[r.Duration], Redeemed: r.Redeemed, Active: r.Active, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.PercentBps != nil {
		c.PercentBps = *r.PercentBps
	}
	if r.AmountMinor != nil {
		c.AmountMinor = *r.AmountMinor
	}
	if r.Currency != nil {
		c.Currency = *r.Currency
	}
	if r.DurationPeriods != nil {
		c.DurationPeriods = *r.DurationPeriods
	}
	if r.MaxRedemptions != nil {
		c.MaxRedemptions = *r.MaxRedemptions
	}
	if r.ExpiresAt != nil {
		c.ExpireTime = timestamppb.New(*r.ExpiresAt)
	}
	return c
}

func (s *Server) CreateCoupon(ctx context.Context, req *plansv1.CreateCouponRequest) (*plansv1.CreateCouponResponse, error) {
	c := req.GetCoupon()
	var v domain.Violations
	if !couponCodeRe.MatchString(c.GetCode()) {
		v.Add("coupon.code", "2–40 letters, digits, - or _")
	}
	p := sqlc.InsertCouponParams{Code: c.GetCode(), Name: strings.TrimSpace(c.GetName())}
	switch {
	case c.GetPercentBps() != 0 && c.GetAmountMinor() != 0:
		v.Add("coupon", "percent_bps or amount_minor, not both")
	case c.GetPercentBps() != 0:
		if c.GetPercentBps() < 1 || c.GetPercentBps() > 10000 {
			v.Add("coupon.percent_bps", "1..10000 (1000 is 10 %)")
		}
		pb := c.GetPercentBps()
		p.PercentBps = &pb
	case c.GetAmountMinor() != 0:
		if c.GetAmountMinor() < 0 {
			v.Add("coupon.amount_minor", "must be positive")
		}
		if _, ok := money.LookupCurrency(c.GetCurrency()); !ok {
			v.Add("coupon.currency", "an amount coupon needs a supported currency")
		}
		am, cur := c.GetAmountMinor(), c.GetCurrency()
		p.AmountMinor, p.Currency = &am, &cur
	default:
		v.Add("coupon", "percent_bps or amount_minor is required")
	}
	dur, ok := durationToDB[c.GetDuration()]
	if !ok {
		v.Add("coupon.duration", "once, repeating or forever")
	}
	p.Duration = dur
	if c.GetDuration() == plansv1.CouponDuration_COUPON_DURATION_REPEATING {
		if c.GetDurationPeriods() < 1 {
			v.Add("coupon.duration_periods", "repeating needs at least 1 period")
		}
		dp := c.GetDurationPeriods()
		p.DurationPeriods = &dp
	} else if c.GetDurationPeriods() != 0 {
		v.Add("coupon.duration_periods", "only for a repeating coupon")
	}
	if c.GetMaxRedemptions() < 0 {
		v.Add("coupon.max_redemptions", "must not be negative")
	} else if c.GetMaxRedemptions() > 0 {
		mr := c.GetMaxRedemptions()
		p.MaxRedemptions = &mr
	}
	if c.GetExpireTime() != nil {
		if err := c.GetExpireTime().CheckValid(); err != nil || !c.GetExpireTime().AsTime().After(s.now()) {
			v.Add("coupon.expire_time", "must be a time in the future")
		} else {
			e := c.GetExpireTime().AsTime()
			p.ExpiresAt = &e
		}
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	var out *plansv1.Coupon
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		r, err := q.InsertCoupon(ctx, p)
		if err != nil {
			return err
		}
		out = couponMessage(sqlc.GetCouponRow(r))
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return &plansv1.CreateCouponResponse{Coupon: out}, nil
}

func (s *Server) ListCoupons(ctx context.Context, _ *plansv1.ListCouponsRequest) (*plansv1.ListCouponsResponse, error) {
	resp := &plansv1.ListCouponsResponse{}
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListCoupons(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			resp.Coupons = append(resp.Coupons, couponMessage(sqlc.GetCouponRow(r)))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return resp, nil
}

func (s *Server) DeactivateCoupon(ctx context.Context, req *plansv1.DeactivateCouponRequest) (*plansv1.DeactivateCouponResponse, error) {
	var out *plansv1.Coupon
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		if n, err := q.DeactivateCoupon(ctx, req.GetCode()); err != nil {
			return err
		} else if n == 0 {
			return status.Error(codes.NotFound, "coupon not found")
		}
		r, err := q.GetCoupon(ctx, req.GetCode())
		if err != nil {
			return err
		}
		out = couponMessage(r)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return &plansv1.DeactivateCouponResponse{Coupon: out}, nil
}

// redeemCoupon puts a coupon on a subscription: it must be active, unexpired, not used up, and an amount coupon must be in the subscription's currency.
// A subscription a provider renews cannot have one (its fee is the provider's; use a discount at the provider).
func (s *Server) redeemCoupon(ctx context.Context, q *sqlc.Queries, sub sqlc.GetSubscriptionRow, code string) error {
	if sub.Owner == "provider" || sub.FeeTiming != "advance" {
		return status.Error(codes.FailedPrecondition, "a coupon takes something off a fee Quotify invoices in advance; this subscription's fee is billed another way")
	}
	if _, err := q.GetSubscriptionCoupon(ctx, sub.ID); err == nil {
		return status.Error(codes.FailedPrecondition, "the subscription has a coupon already; remove it first")
	}
	c, err := q.GetCoupon(ctx, code)
	if err != nil {
		return convert.Err(err, "coupon")
	}
	if c.AmountMinor != nil && (c.Currency == nil || *c.Currency != sub.Currency) {
		return status.Errorf(codes.FailedPrecondition, "coupon %s is an amount in another currency than the subscription's %s", code, sub.Currency)
	}
	if n, err := q.RedeemCoupon(ctx, sqlc.RedeemCouponParams{Code: code, Now: s.now()}); err != nil {
		return err
	} else if n == 0 {
		return status.Errorf(codes.FailedPrecondition, "coupon %s cannot be redeemed: it is inactive, expired or used up", code)
	}
	return q.InsertSubscriptionCoupon(ctx, sqlc.InsertSubscriptionCouponParams{SubscriptionID: sub.ID, CouponCode: code})
}

func (s *Server) ApplyCoupon(ctx context.Context, req *plansv1.ApplyCouponRequest) (*plansv1.ApplyCouponResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	if req.GetCode() == "" {
		return nil, domain.Invalid("code", "required")
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		if !row.OpenEnded {
			return status.Error(codes.FailedPrecondition, "an ended subscription cannot take a coupon")
		}
		if err := s.redeemCoupon(ctx, q, row, req.GetCode()); err != nil {
			return err
		}
		out = subscriptionMessage(row, s.now())
		return attachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.ApplyCouponResponse{Subscription: out}, nil
}

func (s *Server) RemoveCoupon(ctx context.Context, req *plansv1.RemoveCouponRequest) (*plansv1.RemoveCouponResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Subscription
	err = s.tx(ctx, func(q *sqlc.Queries) error {
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
		out = subscriptionMessage(r, s.now())
		return attachExtras(ctx, q, out)
	})
	if err != nil {
		return nil, convert.Err(err, "subscription")
	}
	return &plansv1.RemoveCouponResponse{Subscription: out}, nil
}

// couponDiscount is what the subscription's coupon takes off a fee of this period, and the coupon's code; 0 when none applies.
func couponDiscount(ctx context.Context, q *sqlc.Queries, subID uuid.UUID, currency string, fee int64) (int64, string, error) {
	c, err := q.GetSubscriptionCoupon(ctx, subID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", nil
		}
		return 0, "", err
	}
	terms := subscription.CouponTerms{Duration: c.Duration}
	if c.PercentBps != nil {
		terms.PercentBPS = *c.PercentBps
	}
	if c.AmountMinor != nil {
		terms.AmountMinor = *c.AmountMinor
	}
	if c.Currency != nil {
		terms.Currency = *c.Currency
	}
	if c.DurationPeriods != nil {
		terms.DurationPeriods = *c.DurationPeriods
	}
	return subscription.CouponDiscount(fee, terms, currency, c.PeriodsApplied), c.CouponCode, nil
}
