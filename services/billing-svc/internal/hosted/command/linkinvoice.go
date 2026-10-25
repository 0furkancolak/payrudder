package command

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// LinkInvoiceSpec is one use of an ad-hoc payment link: the amount to be paid and who it is for.
type LinkInvoiceSpec struct {
	AmountMinor int64
	// Currency is the tenant's when empty.
	Currency    string
	Description string
	// CustomerExternalID makes the invoice out to this customer; empty makes it a guest invoice (the nil customer id).
	CustomerExternalID string
	// PromotionCode is what the payer typed (or the link carries); empty uses the coupon waiting for the customer, if there is one.
	PromotionCode string
	// PackID and Credits are set for the purchase of a credit pack: the pack restricts some codes, and the credits are what a bonus coupon adds to.
	PackID  string
	Credits int64
}

// CreateLinkInvoice makes the invoice of one use of an ad-hoc link: a single line for the amount, numbered gap-free by the same finalization every invoice goes
// through, so its saga renders the PDF and then waits for the customer to pay (the invoice is collected through the link, not charged to a stored method).
// There is no tax on it: the amount is what the customer pays.
func (b *Backend) CreateLinkInvoice(ctx context.Context, tenantID uuid.UUID, spec LinkInvoiceSpec) (uuid.UUID, error) {
	tn, err := b.in.Admin.GetTenant(inputs.OperatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return uuid.Nil, fmt.Errorf("get tenant: %w", err)
	}
	t := tn.GetTenant()
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return uuid.Nil, err
	}
	currency := inputs.OrDefault(spec.Currency, t.GetCurrency())
	cur, ok := money.LookupCurrency(currency)
	if !ok {
		return uuid.Nil, inputs.Permanent(fmt.Errorf("currency %q is not supported", currency))
	}
	snap := sagamodel.Snapshot{
		TenantName: t.GetName(), Environment: inputs.Environment(t), CurrencyExponent: cur.Exponent, LogoObjectKey: inputs.LogoKey(tenantID, t), SellerAddress: inputs.AddressLines(t.GetAddress()),
		Collection: sagamodel.CollectionLink, Description: spec.Description,
	}
	customerID, country := uuid.Nil, ""
	if spec.CustomerExternalID != "" {
		cu, found, err := b.Customer(ctx, tenantID, spec.CustomerExternalID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("get customer: %w", err)
		}
		if !found {
			return uuid.Nil, inputs.Permanent(fmt.Errorf("customer %q does not exist", spec.CustomerExternalID))
		}
		if customerID, err = uuid.Parse(cu.GetId()); err != nil {
			return uuid.Nil, err
		}
		snap.CustomerExternalID, snap.CustomerName, snap.CustomerEmail, snap.CustomerAddress = cu.GetExternalId(), cu.GetName(), cu.GetEmail(), inputs.AddressLines(cu.GetAddress())
		country = cu.GetCountry()
	}
	snap.Country = country
	snap.SkipMail = snap.CustomerEmail == ""
	body, err := json.Marshal(snap)
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.New()
	lines := []rating.Line{{Kind: rating.KindBaseFee, AmountMinor: spec.AmountMinor, Description: spec.Description}}
	total := spec.AmountMinor
	// A promotion code (or a coupon waiting for the customer) takes something off the price, or adds bonus credits. It is checked first, so that a code that would leave
	// nothing to pay does not use up a redemption; then it is claimed for this invoice, and given back if the invoice is not made.
	var promo *plansv1.RedeemPromotionResponse
	target, promoTarget := spec.target()
	if quote, err := b.redeem(ctx, tenantID, spec, customerID, currency, id, true); err != nil {
		return uuid.Nil, err
	} else if quote != nil {
		if promo, err = b.redeem(ctx, tenantID, spec, customerID, currency, id, false); err != nil {
			return uuid.Nil, err
		}
		if promo != nil && promo.GetDiscountMinor() > 0 {
			lines = append(lines, rating.Line{Kind: rating.KindDiscount, AmountMinor: -promo.GetDiscountMinor(), Description: discountText(promo), CouponCode: promo.GetCouponCode()})
			total = promo.GetTotalMinor()
		}
	}
	err = pgxx.InTx(ctx, b.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := b.q.WithTx(tx)
		// plan_version_id has no meaning for a payment link: the nil id says so.
		if _, err := q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
			TenantID: tenantID, ID: id, CustomerID: customerID, PeriodID: period.PeriodOf(b.cfg.Now(), loc), Kind: "LINK", PlanVersionID: uuid.Nil, Currency: currency,
			SubtotalMinor: total, TotalMinor: total, Snapshot: body, Provider: "", Country: store.NilIfEmpty(country),
		}); err != nil {
			return err
		}
		if err := invoicecmd.InsertLines(ctx, q, tenantID, id, lines, make([]int, len(lines))); err != nil {
			return err
		}
		if promo == nil {
			return nil
		}
		coupon := promo.GetCouponCode()
		return q.SetInvoicePromotion(ctx, sqlc.SetInvoicePromotionParams{TenantID: tenantID, ID: id, CouponCode: &coupon, PromotionCode: store.NilIfEmpty(promo.GetPromotionCode()),
			PromotionTarget: &promoTarget, BonusCredits: promo.GetBonusCredits()})
	})
	if err != nil {
		if promo != nil {
			b.giveBack(ctx, tenantID, id, target)
		}
		return uuid.Nil, err
	}
	if err := b.cfg.Invoices.Finalize(ctx, tenantID, id); err != nil {
		return uuid.Nil, err
	}
	b.cfg.Invoices.Drafted(ctx, 1)
	return id, nil
}
