package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// The two things a promotion code can be used on besides a subscription's fee, as the invoice remembers them.
const (
	PromotionCreditPurchase = "credit_purchase"
	PromotionPayment        = "payment"
)

// PromotionQuote is what a promotion code takes off a purchase.
type PromotionQuote struct {
	Applied       bool
	PromotionCode string
	CouponCode    string
	CouponName    string
	DiscountMinor int64
	BonusCredits  int64
	// TotalMinor is the amount after the discount.
	TotalMinor int64
}

// target is what the spec's purchase is, for plans-svc and for the invoice.
func (s LinkInvoiceSpec) target() (plansv1.CouponTarget, string) {
	if s.PackID != "" {
		return plansv1.CouponTarget_COUPON_TARGET_CREDIT_PURCHASE, PromotionCreditPurchase
	}
	return plansv1.CouponTarget_COUPON_TARGET_PAYMENT, PromotionPayment
}

// wantsPromotion reports whether plans-svc has to be asked at all: a guest who typed nothing has nothing waiting.
func (s LinkInvoiceSpec) wantsPromotion() bool {
	return s.PromotionCode != "" || s.CustomerExternalID != ""
}

// redeem asks plans-svc to check (preview) or claim the spec's promotion for the invoice targetID. The amount and the credits are the link's own, frozen when it was
// made; nothing the payer sends can change them. nil without an error means no promotion applies.
func (b *Backend) redeem(ctx context.Context, tenantID uuid.UUID, spec LinkInvoiceSpec, customerID uuid.UUID, currency string, targetID uuid.UUID, preview bool) (*plansv1.RedeemPromotionResponse, error) {
	if !spec.wantsPromotion() {
		return nil, nil
	}
	target, _ := spec.target()
	prior := false
	if customerID != uuid.Nil {
		var err error
		if prior, err = b.q.CustomerHasPaidInvoice(ctx, sqlc.CustomerHasPaidInvoiceParams{TenantID: tenantID, CustomerID: customerID}); err != nil {
			return nil, err
		}
	}
	req := &plansv1.RedeemPromotionRequest{
		Code: spec.PromotionCode, CustomerExternalId: spec.CustomerExternalID, Target: target, TargetId: targetID.String(), AmountMinor: spec.AmountMinor, Currency: currency,
		Item: spec.PackID, Credits: spec.Credits, PriorPurchases: prior, Preview: preview,
	}
	if !preview {
		req.InvoiceId = targetID.String()
	}
	resp, err := b.in.Plans.RedeemPromotion(inputs.TenantCtx(ctx, tenantID), req)
	if err != nil {
		return nil, err
	}
	if !resp.GetApplied() {
		return nil, nil
	}
	if resp.GetTotalMinor() <= 0 {
		// A payment of nothing cannot go through a provider; the seller grants a free purchase directly instead.
		return nil, errkey.New(codes.FailedPrecondition, "billing.payment_link.free_total", "this promotion code takes the whole price, which leaves nothing to pay; ask the seller")
	}
	return resp, nil
}

// discountText is the text of the DISCOUNT line a promotion adds to an invoice.
func discountText(p *plansv1.RedeemPromotionResponse) string {
	if p.GetPromotionCode() != "" {
		return "Promotion code " + p.GetPromotionCode()
	}
	return "Coupon " + p.GetCouponCode()
}

// QuotePromotion says what the spec's promotion code would take off, claiming nothing.
func (b *Backend) QuotePromotion(ctx context.Context, tenantID uuid.UUID, spec LinkInvoiceSpec) (PromotionQuote, error) {
	currency := spec.Currency
	if currency == "" {
		var err error
		if currency, err = b.TenantCurrency(ctx, tenantID); err != nil {
			return PromotionQuote{}, err
		}
	}
	customerID := uuid.Nil
	if spec.CustomerExternalID != "" {
		cu, found, err := b.Customer(ctx, tenantID, spec.CustomerExternalID)
		if err != nil {
			return PromotionQuote{}, err
		}
		if !found {
			return PromotionQuote{}, errkey.New(codes.NotFound, "billing.payment_link.customer_not_found", "no such customer")
		}
		if customerID, err = uuid.Parse(cu.GetId()); err != nil {
			return PromotionQuote{}, err
		}
	}
	resp, err := b.redeem(ctx, tenantID, spec, customerID, currency, uuid.Nil, true)
	if err != nil || resp == nil {
		return PromotionQuote{TotalMinor: spec.AmountMinor}, err
	}
	return PromotionQuote{Applied: true, PromotionCode: resp.GetPromotionCode(), CouponCode: resp.GetCouponCode(), CouponName: resp.GetCouponName(), DiscountMinor: resp.GetDiscountMinor(),
		BonusCredits: resp.GetBonusCredits(), TotalMinor: resp.GetTotalMinor()}, nil
}

// giveBack tells plans-svc that the purchase of invoice id will not happen, after it claimed a promotion for it. Best effort: a failure leaves the redemption
// counted against a purchase that does not exist, which an operator sees in the redemption list.
func (b *Backend) giveBack(ctx context.Context, tenantID, id uuid.UUID, target plansv1.CouponTarget) {
	if _, err := b.in.Plans.ReversePromotion(inputs.TenantCtx(ctx, tenantID), &plansv1.ReversePromotionRequest{Target: target, TargetId: id.String()}); err != nil {
		b.cfg.Log.WarnContext(ctx, "promotion not given back", "tenant", tenantID, "invoice", id, "error", err)
	}
}

// reversalBatch is how many queued reversals one pass tells plans-svc about.
const reversalBatch = 100

// ReversePromotions tells plans-svc about the invoices that were voided after they used a coupon (queued in the transaction of the void). Whether the place goes
// back to the count is the code's setting. A reversal plans-svc has nothing for is done as well; an unreachable plans-svc is tried again next pass.
func (b *Backend) ReversePromotions(ctx context.Context) error {
	due, err := b.q.ListPendingPromotionReversals(ctx, reversalBatch)
	if err != nil {
		return err
	}
	for _, d := range due {
		target := plansv1.CouponTarget_COUPON_TARGET_PAYMENT
		if d.TargetKind == PromotionCreditPurchase {
			target = plansv1.CouponTarget_COUPON_TARGET_CREDIT_PURCHASE
		}
		_, err := b.in.Plans.ReversePromotion(inputs.TenantCtx(ctx, d.TenantID), &plansv1.ReversePromotionRequest{Target: target, TargetId: d.InvoiceID.String()})
		if err != nil && status.Code(err) != codes.NotFound {
			b.cfg.Log.WarnContext(ctx, "promotion reversal not delivered", "tenant", d.TenantID, "invoice", d.InvoiceID, "error", err)
			if berr := b.q.BumpPromotionReversalAttempts(ctx, sqlc.BumpPromotionReversalAttemptsParams{TenantID: d.TenantID, InvoiceID: d.InvoiceID}); berr != nil && !errors.Is(berr, pgx.ErrNoRows) {
				return berr
			}
			continue
		}
		if err := b.q.MarkPromotionReversalDone(ctx, sqlc.MarkPromotionReversalDoneParams{TenantID: d.TenantID, InvoiceID: d.InvoiceID}); err != nil {
			return err
		}
	}
	return nil
}
