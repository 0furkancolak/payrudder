package command

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/model"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// ClaimInput is a purchase asking to use a coupon.
type ClaimInput struct {
	// Code is what the customer typed; empty uses the coupon waiting for the customer, if there is one.
	Code string
	// ByCoupon also accepts the coupon's own code, which an operator applying a coupon by hand uses; a customer's typed code must be a promotion code.
	ByCoupon bool
	// CustomerID is uuid.Nil for a guest.
	CustomerID  uuid.UUID
	Target      string
	TargetID    uuid.UUID
	AmountMinor int64
	Currency    string
	Item        string
	Credits     int64
	// PriorPurchases says the customer has paid for something before.
	PriorPurchases bool
	// Preview checks and quotes without claiming anything.
	Preview   bool
	InvoiceID *uuid.UUID
	Now       time.Time
}

// Claimed is the coupon a purchase used, and what it takes off.
type Claimed struct {
	Applied       bool
	RedemptionID  uuid.UUID
	CouponCode    string
	CouponName    string
	PromotionCode string
	DiscountMinor int64
	BonusCredits  int64
	Currency      string
}

// Refusal turns a rejection of the rules into the error a client sees.
func Refusal(r *model.Rejection) error {
	code := codes.FailedPrecondition
	if r.Key == model.KeyNotFound {
		code = codes.NotFound
	}
	return errkey.New(code, r.Key, r.Msg)
}

// Claim checks a purchase against a coupon and, unless it is a preview, uses it: in the caller's transaction it counts the redemption against the code and the
// coupon with conditional updates (so two checkouts cannot both take the last one, however they interleave) and writes the ledger row. Asking again for the same
// purchase returns the redemption made the first time. An error leaves nothing claimed once the caller rolls its transaction back.
func Claim(ctx context.Context, q *sqlc.Queries, in ClaimInput) (Claimed, error) {
	if !in.Preview {
		if live, err := q.GetLiveRedemption(ctx, sqlc.GetLiveRedemptionParams{TargetKind: in.Target, TargetID: in.TargetID}); err == nil {
			return replay(ctx, q, in, live)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return Claimed{}, err
		}
	}
	cp, pc, grant, err := resolve(ctx, q, in)
	if err != nil || cp == nil {
		return Claimed{}, err
	}
	var uses int32
	if pc != nil && in.CustomerID != uuid.Nil {
		if uses, err = codeUses(ctx, q, pc.ID, in.CustomerID); err != nil {
			return Claimed{}, err
		}
	}
	res, err := evaluate(cp, pc, in, uses)
	if err != nil {
		return Claimed{}, err
	}
	out := Claimed{Applied: true, CouponCode: cp.Code, CouponName: cp.Name, DiscountMinor: res.DiscountMinor, BonusCredits: res.BonusCredits, Currency: in.Currency}
	if pc != nil {
		out.PromotionCode = pc.Code
	}
	if in.Preview {
		return out, nil
	}
	var pcID *uuid.UUID
	if pc != nil {
		// The code's row is locked from here to the end of the transaction, so what follows sees the redemptions of every checkout that came first.
		if n, err := q.ClaimPromotionCode(ctx, sqlc.ClaimPromotionCodeParams{ID: pc.ID, Now: in.Now}); err != nil {
			return Claimed{}, err
		} else if n == 0 {
			return Claimed{}, lost(ctx, q, in, cp, pc)
		}
		pcID = &pc.ID
		if pc.OnePerCustomer && in.CustomerID != uuid.Nil {
			if again, err := codeUses(ctx, q, pc.ID, in.CustomerID); err != nil {
				return Claimed{}, err
			} else if again > 0 {
				return Claimed{}, Refusal(&model.Rejection{Key: model.KeyAlreadyUsed, Msg: "this customer has used this code already"})
			}
		}
	}
	if n, err := q.ClaimCoupon(ctx, sqlc.ClaimCouponParams{ID: cp.ID, Now: in.Now}); err != nil {
		return Claimed{}, err
	} else if n == 0 {
		return Claimed{}, lost(ctx, q, in, cp, pc)
	}
	var customer *uuid.UUID
	if in.CustomerID != uuid.Nil {
		customer = &in.CustomerID
	}
	id, err := q.InsertRedemption(ctx, sqlc.InsertRedemptionParams{CouponID: cp.ID, PromotionCodeID: pcID, CustomerID: customer, TargetKind: in.Target, TargetID: in.TargetID,
		InvoiceID: in.InvoiceID, DiscountMinor: res.DiscountMinor, BonusCredits: res.BonusCredits, Currency: nilIfEmpty(in.Currency)})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		return Claimed{}, errkey.New(codes.Aborted, model.KeyAlreadyApplied, "this purchase is already using a coupon; ask again")
	}
	if err != nil {
		return Claimed{}, err
	}
	if grant != nil {
		if err := q.ConsumeCustomerCoupon(ctx, sqlc.ConsumeCustomerCouponParams{ID: *grant, Now: in.Now, RedemptionID: &id}); err != nil {
			return Claimed{}, err
		}
	}
	out.RedemptionID = id
	return out, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// replay answers a purchase that has a redemption already. A different coupon than the one it used is refused: one coupon per purchase.
func replay(ctx context.Context, q *sqlc.Queries, in ClaimInput, live sqlc.GetLiveRedemptionRow) (Claimed, error) {
	if in.Code != "" {
		cp, _, _, err := resolve(ctx, q, ClaimInput{Code: in.Code, ByCoupon: in.ByCoupon, Now: in.Now, Preview: true})
		if err != nil {
			return Claimed{}, err
		}
		if cp != nil && cp.ID != live.CouponID {
			return Claimed{}, Refusal(&model.Rejection{Key: model.KeyAlreadyApplied, Msg: "this purchase is using another coupon already; one coupon per purchase"})
		}
	}
	return Claimed{Applied: true, RedemptionID: live.ID, CouponCode: live.CouponCode, CouponName: live.CouponName, PromotionCode: live.PromotionCode, DiscountMinor: live.DiscountMinor,
		BonusCredits: live.BonusCredits, Currency: live.Currency}, nil
}

// resolve finds the coupon (and the code, and the waiting grant) the input names. A nil coupon without an error means there is nothing to apply.
func resolve(ctx context.Context, q *sqlc.Queries, in ClaimInput) (*sqlc.GetCouponRow, *couponsqry.PromotionCodeRow, *uuid.UUID, error) {
	notFound := Refusal(&model.Rejection{Key: model.KeyNotFound, Msg: "no such code"})
	if in.Code == "" {
		if in.CustomerID == uuid.Nil {
			return nil, nil, nil, nil
		}
		g, err := q.LockPendingCustomerCoupon(ctx, sqlc.LockPendingCustomerCouponParams{CustomerID: in.CustomerID, Now: in.Now})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, nil
		}
		if err != nil {
			return nil, nil, nil, err
		}
		row, err := q.GetCouponByID(ctx, g.CouponID)
		if err != nil {
			return nil, nil, nil, err
		}
		cp := sqlc.GetCouponRow(row)
		return &cp, nil, &g.ID, nil
	}
	pc, err := q.GetPromotionCodeByText(ctx, in.Code)
	switch {
	case err == nil:
		row, err := q.GetCouponByID(ctx, pc.CouponID)
		if err != nil {
			return nil, nil, nil, err
		}
		cp := sqlc.GetCouponRow(row)
		return &cp, &pc, nil, nil
	case errors.Is(err, pgx.ErrNoRows) && in.ByCoupon:
		cp, err := q.GetCoupon(ctx, in.Code)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, notFound
		}
		if err != nil {
			return nil, nil, nil, err
		}
		return &cp, nil, nil, nil
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil, nil, notFound
	}
	return nil, nil, nil, err
}

func codeUses(ctx context.Context, q *sqlc.Queries, code, customer uuid.UUID) (int32, error) {
	return q.CustomerCodeUses(ctx, sqlc.CustomerCodeUsesParams{PromotionCodeID: &code, CustomerID: &customer})
}

func evaluate(cp *sqlc.GetCouponRow, pc *couponsqry.PromotionCodeRow, in ClaimInput, uses int32) (model.Result, error) {
	var code *model.Code
	if pc != nil {
		code = couponsqry.CodeModel(*pc)
	}
	res, rej := model.Evaluate(couponsqry.CouponModel(*cp), code, model.Purchase{Target: in.Target, AmountMinor: in.AmountMinor, Currency: in.Currency, Item: in.Item, Credits: in.Credits,
		CustomerKnown: in.CustomerID != uuid.Nil, PriorPurchases: in.PriorPurchases, CodeUses: uses, Now: in.Now})
	if rej != nil {
		return res, Refusal(rej)
	}
	return res, nil
}

// lost explains a conditional update that changed nothing: someone else took the last redemption, or the code stopped working in between.
func lost(ctx context.Context, q *sqlc.Queries, in ClaimInput, cp *sqlc.GetCouponRow, pc *couponsqry.PromotionCodeRow) error {
	fresh, err := q.GetCouponByID(ctx, cp.ID)
	if err != nil {
		return err
	}
	c := sqlc.GetCouponRow(fresh)
	var code *couponsqry.PromotionCodeRow
	if pc != nil {
		row, err := q.GetPromotionCodeByText(ctx, pc.Code)
		if err != nil {
			return err
		}
		code = &row
	}
	if _, err := evaluate(&c, code, in, 0); err != nil {
		return err
	}
	return Refusal(&model.Rejection{Key: model.KeyExhausted, Msg: "this code has been used up"})
}

// Reverse gives back the redemption of a purchase that was voided or fully refunded: it is marked reversed, and the place it took in the count of its code and coupon
// is returned only when the code says so (default no, so a voided purchase does not reopen a limited offer). It reports whether there was a redemption to reverse.
func Reverse(ctx context.Context, q *sqlc.Queries, target string, targetID uuid.UUID, now time.Time) (bool, error) {
	row, err := q.LockLiveRedemption(ctx, sqlc.LockLiveRedemptionParams{TargetKind: target, TargetID: targetID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, reverseLocked(ctx, q, sqlc.LockRedemptionByIDRow(row), now)
}

// reverseLocked reverses a redemption this transaction holds the lock of.
func reverseLocked(ctx context.Context, q *sqlc.Queries, row sqlc.LockRedemptionByIDRow, now time.Time) error {
	if row.Restore && row.PromotionCodeID != nil {
		if err := q.ReturnPromotionCodeSlot(ctx, *row.PromotionCodeID); err != nil {
			return err
		}
		if err := q.ReturnCouponSlot(ctx, row.CouponID); err != nil {
			return err
		}
	}
	return q.MarkRedemptionReversed(ctx, sqlc.MarkRedemptionReversedParams{ID: row.ID, Now: now, SlotReturned: row.Restore && row.PromotionCodeID != nil})
}
