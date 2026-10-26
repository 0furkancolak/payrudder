package httpapi

import (
	"net/http"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// promotionRoutes is the table for promotion codes, their redemptions, quotes and customer grants (phase 46). The coupon routes are in subscriptionRoutes.
func (s *Server) promotionRoutes() []Route {
	return []Route{
		{"POST", "/v1/coupons/{code}/promotion-codes", AccessTenant, ratelimit.ClassWrite, s.createPromotionCodes},
		{"GET", "/v1/promotion-codes", AccessTenant, ratelimit.ClassRead, s.listPromotionCodes},
		{"DELETE", "/v1/promotion-codes/{code}", AccessTenant, ratelimit.ClassWrite, s.deactivatePromotionCode},
		{"GET", "/v1/coupon-redemptions", AccessTenant, ratelimit.ClassRead, s.listCouponRedemptions},
		{"POST", "/v1/promotions/quote", AccessTenant, ratelimit.ClassRead, s.quotePromotion},
		{"POST", "/v1/customers/{external_id}/coupons", AccessTenant, ratelimit.ClassWrite, s.grantCustomerCoupon},
	}
}

func (s *Server) createPromotionCodes(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreatePromotionCodesRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CouponCode = r.PathValue("code")
	resp, err := s.d.Plans.CreatePromotionCodes(r.Context(), req)
	respond(w, r, http.StatusCreated, resp, err)
}

// pageParams reads page_size and page_token.
func pageParams(w http.ResponseWriter, r *http.Request) (size int32, token string, ok bool) {
	token = r.URL.Query().Get("page_token")
	size, err := intParam(r.URL.Query(), "page_size")
	if err != nil {
		writeError(w, r, err)
		return 0, "", false
	}
	return size, token, true
}

func (s *Server) listPromotionCodes(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	resp, err := s.d.Plans.ListPromotionCodes(r.Context(), &plansv1.ListPromotionCodesRequest{CouponCode: q.Get("coupon"), State: q.Get("state"), Search: q.Get("search"),
		PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) deactivatePromotionCode(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.DeactivatePromotionCode(r.Context(), &plansv1.DeactivatePromotionCodeRequest{Code: r.PathValue("code")})
	respond(w, r, http.StatusOK, resp.GetCode(), err)
}

func (s *Server) listCouponRedemptions(w http.ResponseWriter, r *http.Request) {
	size, token, ok := pageParams(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	resp, err := s.d.Plans.ListCouponRedemptions(r.Context(), &plansv1.ListCouponRedemptionsRequest{CouponCode: q.Get("coupon"), PromotionCode: q.Get("promotion_code"),
		CustomerExternalId: q.Get("customer_external_id"), PageSize: size, PageToken: token})
	respond(w, r, http.StatusOK, resp, err)
}

// quotePromotion checks a code against a purchase and says what it would take off, claiming nothing: for a tenant's own checkout page.
func (s *Server) quotePromotion(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.RedeemPromotionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Preview = true
	resp, err := s.d.Plans.RedeemPromotion(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) grantCustomerCoupon(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.GrantCustomerCouponRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.CustomerExternalId = r.PathValue("external_id")
	resp, err := s.d.Plans.GrantCustomerCoupon(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
