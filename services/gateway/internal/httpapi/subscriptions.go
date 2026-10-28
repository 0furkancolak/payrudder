package httpapi

import (
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func (s *Server) createSubscription(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CreateSubscriptionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateSubscription(r.Context(), req)
	respond(w, r, http.StatusCreated, resp.GetSubscription(), err)
}

// listSubscriptions is GET /v1/subscriptions?customer=&page_size=&page_token=, newest first. customer is the external id.
func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &plansv1.ListSubscriptionsRequest{CustomerExternalId: q.Get("customer"), PageToken: q.Get("page_token")}
	n, err := intParam(q, "page_size")
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.PageSize = n
	resp, err := s.d.Plans.ListSubscriptions(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetSubscription(r.Context(), &plansv1.GetSubscriptionRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) pauseSubscription(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.PauseSubscription(r.Context(), &plansv1.PauseSubscriptionRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) resumeSubscription(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ResumeSubscription(r.Context(), &plansv1.ResumeSubscriptionRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) scheduleCancellation(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ScheduleCancellation(r.Context(), &plansv1.ScheduleCancellationRequest{SubscriptionId: r.PathValue("id"), Cancel: true})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) withdrawCancellation(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ScheduleCancellation(r.Context(), &plansv1.ScheduleCancellationRequest{SubscriptionId: r.PathValue("id"), Cancel: false})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) changePlan(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ChangePlanRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.ChangePlan(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) previewSubscriptionChange(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ChangeSubscriptionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.PreviewSubscriptionChange(r.Context(), &plansv1.PreviewSubscriptionChangeRequest{Change: req})
	respond(w, r, http.StatusOK, resp.GetQuote(), err)
}

// schedulePriceChange is POST /v1/plans/{code}/price-changes: the body names the versions and the notice.
func (s *Server) schedulePriceChange(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SchedulePriceChangeRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.PlanCode = r.PathValue("code")
	resp, err := s.d.Plans.SchedulePriceChange(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) changeSubscription(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ChangeSubscriptionRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.ChangeSubscription(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) cancelScheduledChange(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.CancelScheduledChange(r.Context(), &plansv1.CancelScheduledChangeRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) applyCoupon(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.ApplyCouponRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	req.SubscriptionId = r.PathValue("id")
	resp, err := s.d.Plans.ApplyCoupon(r.Context(), req)
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) removeCoupon(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.RemoveCoupon(r.Context(), &plansv1.RemoveCouponRequest{SubscriptionId: r.PathValue("id")})
	respond(w, r, http.StatusOK, resp.GetSubscription(), err)
}

func (s *Server) getTaxRates(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.GetTaxRates(r.Context(), &plansv1.GetTaxRatesRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) setTaxRates(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.SetTaxRatesRequest{}
	if err := decode(r, req); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.SetTaxRates(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) createCoupon(w http.ResponseWriter, r *http.Request) {
	c := &plansv1.Coupon{}
	if err := decode(r, c); err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Plans.CreateCoupon(r.Context(), &plansv1.CreateCouponRequest{Coupon: c})
	respond(w, r, http.StatusCreated, resp.GetCoupon(), err)
}

func (s *Server) listCoupons(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.ListCoupons(r.Context(), &plansv1.ListCouponsRequest{})
	respond(w, r, http.StatusOK, resp, err)
}

func (s *Server) deactivateCoupon(w http.ResponseWriter, r *http.Request) {
	resp, err := s.d.Plans.DeactivateCoupon(r.Context(), &plansv1.DeactivateCouponRequest{Code: r.PathValue("code")})
	respond(w, r, http.StatusOK, resp.GetCoupon(), err)
}

func (s *Server) cancelSubscription(w http.ResponseWriter, r *http.Request) {
	req := &plansv1.CancelSubscriptionRequest{SubscriptionId: r.PathValue("id")}
	if v := r.URL.Query().Get("end_time"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, r, invalid("end_time", "RFC 3339 timestamp"))
			return
		}
		req.EndTime = timestamppb.New(t)
	}
	switch r.URL.Query().Get("credit_unused") {
	case "", "false":
	case "true":
		req.CreditUnused = true
	default:
		writeError(w, r, invalid("credit_unused", "true or false"))
		return
	}
	// Always {subscription, credited_minor}; credited_minor is "0" unless credit_unused credited something.
	resp, err := s.d.Plans.CancelSubscription(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
