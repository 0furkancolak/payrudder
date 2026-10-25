package command

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// CouponSource is the source a coupon granted to a referred customer carries in customer_coupons; the affiliate's id is its reference.
const CouponSource = "affiliate"

// AttributeInput says that a customer came through an affiliate.
type AttributeInput struct {
	// CustomerID is the internal id of an existing customer.
	CustomerID uuid.UUID
	// Code is the affiliate's code (a link, a cookie, the API); AffiliateID names the affiliate directly when a promotion code of theirs was redeemed.
	Code        string
	AffiliateID uuid.UUID
	// Source is link, code or api.
	Source string
	// TouchedAt is when the visitor followed the link or typed the code; the zero time is now.
	TouchedAt time.Time
	Now       time.Time
}

// AttributeResult is what became of an attribution.
type AttributeResult struct {
	Attributed bool
	// Reason is why not (model.Reason*); empty when attributed.
	Reason      string
	AffiliateID uuid.UUID
}

func refused(reason string) AttributeResult { return AttributeResult{Reason: reason} }

// Attribute writes the attribution of a customer in the caller's transaction. The first touch wins: a customer who has an attribution keeps it, whatever comes
// later, and the database refuses to change it. Nothing is attributed when the program is not active, the affiliate is not, the touch is older than the program's
// window, or the customer looks like the affiliate (the same mailbox, the same company domain, a payment method of the affiliate's own): those are logged, and the
// last three are kept in affiliate_attribution_rejections so the owner can see why there is no commission. A refusal is a result, not an error.
func Attribute(ctx context.Context, q *sqlc.Queries, in AttributeInput) (AttributeResult, error) {
	prog, err := q.GetAffiliateProgram(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return refused(model.ReasonProgramInactive), nil
	}
	if err != nil {
		return AttributeResult{}, err
	}
	var aff sqlc.GetAffiliateRow
	switch {
	case in.AffiliateID != uuid.Nil:
		aff, err = q.GetAffiliate(ctx, in.AffiliateID)
	default:
		var byCode sqlc.GetAffiliateByCodeRow
		byCode, err = q.GetAffiliateByCode(ctx, strings.TrimSpace(in.Code))
		aff = sqlc.GetAffiliateRow(byCode)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return refused(model.ReasonUnknownCode), nil
	}
	if err != nil {
		return AttributeResult{}, err
	}
	if prog.Status != model.ProgramActive {
		return refused(model.ReasonProgramInactive), nil
	}
	if aff.Status != model.AffiliateActive {
		return refused(model.ReasonAffiliateInactive), nil
	}
	if _, err := q.GetAttribution(ctx, in.CustomerID); err == nil {
		return AttributeResult{Reason: model.ReasonAlreadyAttributed, AffiliateID: aff.ID}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return AttributeResult{}, err
	}
	now := in.Now
	touched := in.TouchedAt
	if touched.IsZero() {
		touched = now
	}
	if !model.WithinWindow(touched, now, prog.AttributionWindowDays) {
		return refused(model.ReasonExpired), nil
	}
	cust, err := q.GetCustomerForAttribution(ctx, in.CustomerID)
	if err != nil {
		return AttributeResult{}, err
	}
	pms, err := q.PaymentMethodsOfEmail(ctx, aff.Email)
	if err != nil {
		return AttributeResult{}, err
	}
	var affPMs []string
	for _, pm := range pms {
		if pm != nil {
			affPMs = append(affPMs, *pm)
		}
	}
	custPM := ""
	if cust.PaymentMethod != nil {
		custPM = *cust.PaymentMethod
	}
	if reason := model.SelfReferral(aff.Email, cust.Email, affPMs, custPM); reason != "" {
		slog.WarnContext(ctx, "affiliate attribution refused: the customer looks like the affiliate", "affiliate_id", aff.ID, "customer_id", cust.ID, "reason", reason)
		if err := q.InsertAttributionRejection(ctx, sqlc.InsertAttributionRejectionParams{AffiliateID: aff.ID, CustomerID: cust.ID, Reason: reason}); err != nil {
			return AttributeResult{}, err
		}
		return AttributeResult{Reason: reason, AffiliateID: aff.ID}, nil
	}
	source := in.Source
	if source == "" {
		source = model.SourceAPI
	}
	n, err := q.InsertAttribution(ctx, sqlc.InsertAttributionParams{CustomerID: cust.ID, AffiliateID: aff.ID, Source: source, TouchedAt: touched})
	if err != nil {
		return AttributeResult{}, err
	}
	if n == 0 {
		// Another request attributed the customer between the read and the insert; theirs stays.
		return AttributeResult{Reason: model.ReasonAlreadyAttributed, AffiliateID: aff.ID}, nil
	}
	if prog.ReferredCouponID != nil {
		if _, err := couponscmd.GrantToCustomer(ctx, q, cust.ID, *prog.ReferredCouponID, CouponSource, aff.ID.String()); err != nil {
			return AttributeResult{}, err
		}
	}
	return AttributeResult{Attributed: true, AffiliateID: aff.ID}, nil
}

// Attributor is what other modules call to attribute a customer inside their own transaction.
type Attributor func(ctx context.Context, q *sqlc.Queries, in AttributeInput) (AttributeResult, error)

// AttributeFunc is Attribute as an Attributor, for modules that take one.
func (c *Commands) AttributeFunc() Attributor { return Attribute }

// AttributeCustomer is the API call: it attributes the customer named in the request.
func (c *Commands) AttributeCustomer(ctx context.Context, req *plansv1.AttributeCustomerRequest) (*plansv1.AttributeCustomerResponse, error) {
	code := strings.TrimSpace(req.GetCode())
	if code == "" {
		return nil, errkey.New(codes.InvalidArgument, "affiliate.code_required", "a code is required")
	}
	switch req.GetSource() {
	case "", model.SourceLink, model.SourceCode, model.SourceAPI:
	default:
		return nil, errkey.New(codes.InvalidArgument, "affiliate.bad_source", "source is link, code or api")
	}
	var res AttributeResult
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var cust sqlc.GetCustomerForAttributionRow
		var err error
		switch {
		case req.GetCustomerId() != "":
			id, perr := uuid.Parse(req.GetCustomerId())
			if perr != nil {
				return errkey.New(codes.NotFound, "affiliate.customer_not_found", "no such customer")
			}
			cust, err = q.GetCustomerForAttribution(ctx, id)
		case req.GetCustomerExternalId() != "":
			var byExt sqlc.GetCustomerForAttributionByExternalIDRow
			byExt, err = q.GetCustomerForAttributionByExternalID(ctx, req.GetCustomerExternalId())
			cust = sqlc.GetCustomerForAttributionRow(byExt)
		default:
			return errkey.New(codes.InvalidArgument, "affiliate.customer_required", "customer_external_id is required")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.NotFound, "affiliate.customer_not_found", "no such customer")
		}
		if err != nil {
			return err
		}
		in := AttributeInput{CustomerID: cust.ID, Code: code, Source: req.GetSource(), Now: c.now()}
		if t := req.GetTouchTime(); t != nil {
			in.TouchedAt = t.AsTime()
		}
		res, err = Attribute(ctx, q, in)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return &plansv1.AttributeCustomerResponse{Attributed: res.Attributed, Reason: res.Reason}, nil
}

// AttributeByPromotionCode is the hook of a checkout that redeemed a promotion code: when the code belongs to an affiliate, the customer is attributed to them
// (source "code"). A code that belongs to nobody, or an attribution that is refused, changes nothing.
func AttributeByPromotionCode(ctx context.Context, q *sqlc.Queries, customerID uuid.UUID, promotionCode string, now time.Time) error {
	owner, err := q.GetPromotionCodeAffiliate(ctx, promotionCode)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = Attribute(ctx, q, AttributeInput{CustomerID: customerID, AffiliateID: *owner, Source: model.SourceCode, Now: now})
	return err
}
