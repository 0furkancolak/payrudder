// Package coupons registers the coupon handlers of PlansService: request to command or query, result to response.
package coupons

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/coupons/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/coupons/query"
)

// Handler serves the coupon methods of PlansService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the coupon handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateCoupon(ctx context.Context, req *plansv1.CreateCouponRequest) (*plansv1.CreateCouponResponse, error) {
	c, err := h.cmd.Create(ctx, req.GetCoupon())
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateCouponResponse{Coupon: c}, nil
}

func (h *Handler) ListCoupons(ctx context.Context, _ *plansv1.ListCouponsRequest) (*plansv1.ListCouponsResponse, error) {
	cs, err := h.qry.List(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.ListCouponsResponse{Coupons: cs}, nil
}

func (h *Handler) DeactivateCoupon(ctx context.Context, req *plansv1.DeactivateCouponRequest) (*plansv1.DeactivateCouponResponse, error) {
	c, err := h.cmd.Deactivate(ctx, req.GetCode())
	if err != nil {
		return nil, err
	}
	return &plansv1.DeactivateCouponResponse{Coupon: c}, nil
}

func (h *Handler) ApplyCoupon(ctx context.Context, req *plansv1.ApplyCouponRequest) (*plansv1.ApplyCouponResponse, error) {
	s, err := h.cmd.Apply(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ApplyCouponResponse{Subscription: s}, nil
}

func (h *Handler) RemoveCoupon(ctx context.Context, req *plansv1.RemoveCouponRequest) (*plansv1.RemoveCouponResponse, error) {
	s, err := h.cmd.Remove(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.RemoveCouponResponse{Subscription: s}, nil
}
