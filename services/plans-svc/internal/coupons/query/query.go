// Package query reads coupons, promotion codes and redemptions, and works out what a coupon takes off a fee.
package query

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads coupons.
type Queries struct{ db *store.DB }

// New returns the coupon queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

var durationFromDB = map[string]plansv1.CouponDuration{
	subscription.CouponOnce:      plansv1.CouponDuration_COUPON_DURATION_ONCE,
	subscription.CouponRepeating: plansv1.CouponDuration_COUPON_DURATION_REPEATING,
	subscription.CouponForever:   plansv1.CouponDuration_COUPON_DURATION_FOREVER,
}

// TargetToDB and TargetFromDB map the coupon targets between the API and the stored names.
var (
	TargetToDB = map[plansv1.CouponTarget]string{
		plansv1.CouponTarget_COUPON_TARGET_SUBSCRIPTION:    model.TargetSubscription,
		plansv1.CouponTarget_COUPON_TARGET_CREDIT_PURCHASE: model.TargetCreditPurchase,
		plansv1.CouponTarget_COUPON_TARGET_PAYMENT:         model.TargetPayment,
	}
	TargetFromDB = map[string]plansv1.CouponTarget{
		model.TargetSubscription:   plansv1.CouponTarget_COUPON_TARGET_SUBSCRIPTION,
		model.TargetCreditPurchase: plansv1.CouponTarget_COUPON_TARGET_CREDIT_PURCHASE,
		model.TargetPayment:        plansv1.CouponTarget_COUPON_TARGET_PAYMENT,
	}
)

// Message maps a coupon row to its API message.
func Message(r sqlc.GetCouponRow) *plansv1.Coupon {
	c := &plansv1.Coupon{
		Id: r.ID.String(), Code: r.Code, Name: r.Name, Duration: durationFromDB[r.Duration], Redeemed: r.Redeemed, Active: r.Active, CreateTime: timestamppb.New(r.CreatedAt),
		PromotionCodeCount: r.PromotionCodeCount,
	}
	for _, t := range r.AppliesTo {
		c.AppliesTo = append(c.AppliesTo, TargetFromDB[t])
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
	if r.BonusPercentBps != nil {
		c.BonusPercentBps = *r.BonusPercentBps
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

// CouponModel is the coupon as the rules see it.
func CouponModel(r sqlc.GetCouponRow) model.Coupon {
	c := model.Coupon{Active: r.Active, ExpiresAt: r.ExpiresAt, MaxRedeemed: r.MaxRedemptions, Redeemed: r.Redeemed, AppliesTo: r.AppliesTo}
	if r.PercentBps != nil {
		c.PercentBPS = *r.PercentBps
	}
	if r.AmountMinor != nil {
		c.AmountMinor = *r.AmountMinor
	}
	if r.Currency != nil {
		c.Currency = *r.Currency
	}
	if r.BonusPercentBps != nil {
		c.BonusBPS = *r.BonusPercentBps
	}
	return c
}

// PromotionCodeRow is a promotion code row, whichever query read it.
type PromotionCodeRow = sqlc.GetPromotionCodeByTextRow

// PromotionMessage maps a promotion code row to its API message.
func PromotionMessage(r PromotionCodeRow) *plansv1.PromotionCode {
	p := &plansv1.PromotionCode{
		Id: r.ID.String(), CouponId: r.CouponID.String(), CouponCode: r.CouponCode, Code: r.Code, Active: r.Active, Redeemed: r.Redeemed, FirstPurchaseOnly: r.FirstPurchaseOnly,
		OnePerCustomer: r.OnePerCustomer, AllowedPlans: r.AllowedPlans, AllowedPacks: r.AllowedPacks, AllowedCurrencies: r.AllowedCurrencies, RestoreOnReversal: r.RestoreOnReversal,
		CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.MaxRedemptions != nil {
		p.MaxRedemptions = *r.MaxRedemptions
	}
	if r.ExpiresAt != nil {
		p.ExpireTime = timestamppb.New(*r.ExpiresAt)
	}
	if r.MinAmountMinor != nil {
		p.MinAmountMinor = *r.MinAmountMinor
	}
	if r.MinAmountCurrency != nil {
		p.MinAmountCurrency = *r.MinAmountCurrency
	}
	return p
}

// CodeModel is the promotion code as the rules see it.
func CodeModel(r PromotionCodeRow) *model.Code {
	c := &model.Code{Active: r.Active, ExpiresAt: r.ExpiresAt, MaxRedeemed: r.MaxRedemptions, Redeemed: r.Redeemed, FirstPurchaseOnly: r.FirstPurchaseOnly, OnePerCustomer: r.OnePerCustomer,
		AllowedPlans: r.AllowedPlans, AllowedPacks: r.AllowedPacks, AllowedCurrencies: r.AllowedCurrencies}
	if r.MinAmountMinor != nil {
		c.MinAmountMinor = *r.MinAmountMinor
	}
	if r.MinAmountCurrency != nil {
		c.MinAmountCurrency = *r.MinAmountCurrency
	}
	return c
}

// List returns the tenant's coupons.
func (r *Queries) List(ctx context.Context) ([]*plansv1.Coupon, error) {
	var out []*plansv1.Coupon
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListCoupons(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, Message(sqlc.GetCouponRow(row)))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "coupon")
	}
	return out, nil
}

const defaultPage, maxPage = 50, 200

// page turns a page size and token (an offset) into a limit and offset.
func page(size int32, token string) (limit, offset int32, err error) {
	limit = size
	if limit <= 0 {
		limit = defaultPage
	}
	limit = min(limit, maxPage)
	if token != "" {
		n, perr := strconv.ParseInt(token, 10, 32)
		if perr != nil || n < 0 {
			return 0, 0, errkey.New(codes.InvalidArgument, "plans.coupon.bad_page_token", "page_token is not one this API gave")
		}
		offset = int32(n)
	}
	return limit, offset, nil
}

func nextToken(offset, limit int32, total int64) string {
	if int64(offset)+int64(limit) >= total {
		return ""
	}
	return strconv.Itoa(int(offset + limit))
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ListPromotionCodes returns a page of promotion codes, newest first, and the total.
func (r *Queries) ListPromotionCodes(ctx context.Context, req *plansv1.ListPromotionCodesRequest) (*plansv1.ListPromotionCodesResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	p := sqlc.ListPromotionCodesParams{CouponCode: nilIfEmpty(req.GetCouponCode()), Search: nilIfEmpty(req.GetSearch()), RowLimit: limit, RowOffset: offset}
	switch req.GetState() {
	case "":
	case "active", "inactive":
		a := req.GetState() == "active"
		p.Active = &a
	default:
		return nil, errkey.New(codes.InvalidArgument, "plans.coupon.bad_state", "state is active, inactive or empty")
	}
	out := &plansv1.ListPromotionCodesResponse{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListPromotionCodes(ctx, p)
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
			out.Codes = append(out.Codes, PromotionMessage(PromotionCodeRow{
				ID: row.ID, CouponID: row.CouponID, CouponCode: row.CouponCode, Code: row.Code, Active: row.Active, MaxRedemptions: row.MaxRedemptions, Redeemed: row.Redeemed,
				ExpiresAt: row.ExpiresAt, FirstPurchaseOnly: row.FirstPurchaseOnly, OnePerCustomer: row.OnePerCustomer, MinAmountMinor: row.MinAmountMinor,
				MinAmountCurrency: row.MinAmountCurrency, AllowedPlans: row.AllowedPlans, AllowedPacks: row.AllowedPacks, AllowedCurrencies: row.AllowedCurrencies,
				RestoreOnReversal: row.RestoreOnReversal, CreatedAt: row.CreatedAt,
			}))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "promotion code")
	}
	out.Total, out.NextPageToken = int32(total), nextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}

// ListRedemptions returns a page of redemptions, newest first, and the total.
func (r *Queries) ListRedemptions(ctx context.Context, req *plansv1.ListCouponRedemptionsRequest) (*plansv1.ListCouponRedemptionsResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	p := sqlc.ListCouponRedemptionsParams{CouponCode: nilIfEmpty(req.GetCouponCode()), PromotionCode: nilIfEmpty(req.GetPromotionCode()),
		CustomerExternalID: nilIfEmpty(req.GetCustomerExternalId()), RowLimit: limit, RowOffset: offset}
	out := &plansv1.ListCouponRedemptionsResponse{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListCouponRedemptions(ctx, p)
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
			m := &plansv1.CouponRedemption{
				Id: row.ID.String(), CouponCode: row.CouponCode, PromotionCode: row.PromotionCode, CustomerExternalId: row.CustomerExternalID, Target: TargetFromDB[row.TargetKind],
				TargetId: row.TargetID.String(), DiscountMinor: row.DiscountMinor, BonusCredits: row.BonusCredits, Currency: row.Currency, CreateTime: timestamppb.New(row.CreatedAt),
			}
			if row.InvoiceID != nil {
				m.InvoiceId = row.InvoiceID.String()
			}
			if row.ReversedAt != nil {
				m.ReverseTime = timestamppb.New(*row.ReversedAt)
			}
			out.Redemptions = append(out.Redemptions, m)
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "redemption")
	}
	out.Total, out.NextPageToken = int32(total), nextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}

// Discount is what the subscription's coupon takes off a fee of this period, and the coupon's code; 0 when none applies.
func Discount(ctx context.Context, q *sqlc.Queries, subID uuid.UUID, currency string, fee int64) (int64, string, error) {
	c, err := q.GetSubscriptionCoupon(ctx, subID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", nil
		}
		return 0, "", err
	}
	return subscription.CouponDiscount(fee, termsOf(c), currency, c.PeriodsApplied), c.CouponCode, nil
}

func termsOf(c sqlc.GetSubscriptionCouponRow) subscription.CouponTerms {
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
	return terms
}

// InForce returns what the subscription's coupon took off the fee of the period that is in force, which is already invoiced and so already counted in the
// coupon's periods (Discount is for the next one). known is false when the counter cannot say whether the period was discounted: a coupon that has used all
// its periods may have been used up by this one or earlier.
func InForce(ctx context.Context, q *sqlc.Queries, subID uuid.UUID, currency string) (discount func(fee int64) int64, known bool, err error) {
	none := func(int64) int64 { return 0 }
	c, err := q.GetSubscriptionCoupon(ctx, subID)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	terms := termsOf(c)
	// A probe fee: whether the coupon still has periods to give does not depend on the amount.
	live := subscription.CouponDiscount(1_000_000, terms, currency, c.PeriodsApplied) > 0
	switch {
	case c.PeriodsApplied == 0:
		return none, true, nil // the period in force was invoiced before the coupon had been used
	case !live:
		return nil, false, nil
	}
	return func(fee int64) int64 { return subscription.CouponDiscount(fee, terms, currency, c.PeriodsApplied-1) }, true, nil
}
