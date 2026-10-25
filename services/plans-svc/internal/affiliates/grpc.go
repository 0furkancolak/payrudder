// Package affiliates registers the handlers of the affiliate program (phase 47): AffiliateService for the tenant, AffiliatePublicService for the public link.
// The rules are in the command and query packages; a handler maps a request to one of them and the result to a response.
package affiliates

import (
	"context"
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
)

// Handler serves plansv1.AffiliateService.
type Handler struct {
	plansv1.UnimplementedAffiliateServiceServer
	cmd *command.Commands
	qry *query.Queries
}

// New returns the affiliate handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetAffiliateProgram(ctx context.Context, _ *plansv1.GetAffiliateProgramRequest) (*plansv1.GetAffiliateProgramResponse, error) {
	p, err := h.qry.Program(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetAffiliateProgramResponse{Program: p}, nil
}

func (h *Handler) SetAffiliateProgram(ctx context.Context, req *plansv1.SetAffiliateProgramRequest) (*plansv1.SetAffiliateProgramResponse, error) {
	p, err := h.cmd.SetProgram(ctx, req.GetProgram())
	if err != nil {
		return nil, err
	}
	return &plansv1.SetAffiliateProgramResponse{Program: p}, nil
}

func (h *Handler) CreateAffiliate(ctx context.Context, req *plansv1.CreateAffiliateRequest) (*plansv1.CreateAffiliateResponse, error) {
	a, err := h.cmd.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateAffiliateResponse{Affiliate: a}, nil
}

func (h *Handler) ListAffiliates(ctx context.Context, req *plansv1.ListAffiliatesRequest) (*plansv1.ListAffiliatesResponse, error) {
	return h.qry.List(ctx, req)
}

func (h *Handler) GetAffiliate(ctx context.Context, req *plansv1.GetAffiliateRequest) (*plansv1.GetAffiliateResponse, error) {
	a, err := h.qry.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetAffiliateResponse{Affiliate: a}, nil
}

func (h *Handler) UpdateAffiliate(ctx context.Context, req *plansv1.UpdateAffiliateRequest) (*plansv1.UpdateAffiliateResponse, error) {
	a, err := h.cmd.Update(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.UpdateAffiliateResponse{Affiliate: a}, nil
}

func (h *Handler) LinkAffiliatePromotionCode(ctx context.Context, req *plansv1.LinkAffiliatePromotionCodeRequest) (*plansv1.LinkAffiliatePromotionCodeResponse, error) {
	a, err := h.cmd.LinkPromotionCode(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.LinkAffiliatePromotionCodeResponse{Affiliate: a}, nil
}

func (h *Handler) AttributeCustomer(ctx context.Context, req *plansv1.AttributeCustomerRequest) (*plansv1.AttributeCustomerResponse, error) {
	return h.cmd.AttributeCustomer(ctx, req)
}

func (h *Handler) ListAffiliateReferrals(ctx context.Context, req *plansv1.ListAffiliateReferralsRequest) (*plansv1.ListAffiliateReferralsResponse, error) {
	return h.qry.Referrals(ctx, req)
}

func (h *Handler) ListAffiliateCommissions(ctx context.Context, req *plansv1.ListAffiliateCommissionsRequest) (*plansv1.ListAffiliateCommissionsResponse, error) {
	return h.qry.Commissions(ctx, req)
}

func (h *Handler) ReverseAffiliateCommission(ctx context.Context, req *plansv1.ReverseAffiliateCommissionRequest) (*plansv1.ReverseAffiliateCommissionResponse, error) {
	return h.cmd.ReverseCommission(ctx, req)
}

func (h *Handler) GetAffiliateBalances(ctx context.Context, req *plansv1.GetAffiliateBalancesRequest) (*plansv1.GetAffiliateBalancesResponse, error) {
	return h.qry.Balances(ctx, req.GetAffiliateId())
}

func (h *Handler) GetAffiliateFunnel(ctx context.Context, req *plansv1.GetAffiliateFunnelRequest) (*plansv1.GetAffiliateFunnelResponse, error) {
	return h.qry.Funnel(ctx, req, time.Now())
}

func (h *Handler) ListAffiliatePayouts(ctx context.Context, req *plansv1.ListAffiliatePayoutsRequest) (*plansv1.ListAffiliatePayoutsResponse, error) {
	return h.qry.Payouts(ctx, req)
}

func (h *Handler) CreateAffiliatePayouts(ctx context.Context, req *plansv1.CreateAffiliatePayoutsRequest) (*plansv1.CreateAffiliatePayoutsResponse, error) {
	return h.cmd.CreatePayouts(ctx, req)
}

func (h *Handler) MarkAffiliatePayoutPaid(ctx context.Context, req *plansv1.MarkAffiliatePayoutPaidRequest) (*plansv1.MarkAffiliatePayoutPaidResponse, error) {
	p, err := h.cmd.MarkPaid(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.MarkAffiliatePayoutPaidResponse{Payout: p}, nil
}

func (h *Handler) CancelAffiliatePayout(ctx context.Context, req *plansv1.CancelAffiliatePayoutRequest) (*plansv1.CancelAffiliatePayoutResponse, error) {
	p, err := h.cmd.CancelPayout(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CancelAffiliatePayoutResponse{Payout: p}, nil
}

func (h *Handler) ListAffiliateJournal(ctx context.Context, req *plansv1.ListAffiliateJournalRequest) (*plansv1.ListAffiliateJournalResponse, error) {
	return h.qry.Journal(ctx, req)
}

// PublicHandler serves plansv1.AffiliatePublicService.
type PublicHandler struct {
	plansv1.UnimplementedAffiliatePublicServiceServer
	pub *command.Public
}

// NewPublic returns the handler of the public link.
func NewPublic(pub *command.Public) *PublicHandler { return &PublicHandler{pub: pub} }

func (h *PublicHandler) TrackAffiliateClick(ctx context.Context, req *plansv1.TrackAffiliateClickRequest) (*plansv1.TrackAffiliateClickResponse, error) {
	return h.pub.TrackClick(ctx, req)
}
