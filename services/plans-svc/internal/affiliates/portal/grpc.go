// Package portal registers the handlers of AffiliatePortalService (phase 47): what a signed-in partner sees and does with their own affiliations. The gateway resolves the
// session and passes the person's id; every call finds its data only through that person's own partner record.
package portal

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/portal/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/portal/query"
)

// Handler serves plansv1.AffiliatePortalService.
type Handler struct {
	plansv1.UnimplementedAffiliatePortalServiceServer
	cmd *command.Commands
	qry *query.Queries
}

// New returns the portal handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) ListMyAffiliations(ctx context.Context, req *plansv1.ListMyAffiliationsRequest) (*plansv1.ListMyAffiliationsResponse, error) {
	as, err := h.qry.Affiliations(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListMyAffiliationsResponse{Affiliations: as}, nil
}

func (h *Handler) ClaimMyAffiliations(ctx context.Context, req *plansv1.ClaimMyAffiliationsRequest) (*plansv1.ClaimMyAffiliationsResponse, error) {
	n, err := h.cmd.Claim(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	as, err := h.qry.Affiliations(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ClaimMyAffiliationsResponse{Claimed: n, Affiliations: as}, nil
}

func (h *Handler) LookupAffiliateProgram(ctx context.Context, req *plansv1.LookupAffiliateProgramRequest) (*plansv1.LookupAffiliateProgramResponse, error) {
	return h.qry.Program(ctx, req.GetUserId(), req.GetTenantId())
}

func (h *Handler) ApplyAsAffiliate(ctx context.Context, req *plansv1.ApplyAsAffiliateRequest) (*plansv1.ApplyAsAffiliateResponse, error) {
	a, err := h.cmd.Apply(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ApplyAsAffiliateResponse{Affiliation: a}, nil
}

func (h *Handler) GetMyAffiliation(ctx context.Context, req *plansv1.GetMyAffiliationRequest) (*plansv1.GetMyAffiliationResponse, error) {
	return h.qry.Get(ctx, req)
}

func (h *Handler) UpdateMyAffiliation(ctx context.Context, req *plansv1.UpdateMyAffiliationRequest) (*plansv1.UpdateMyAffiliationResponse, error) {
	a, err := h.cmd.Update(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.UpdateMyAffiliationResponse{Affiliation: a}, nil
}

func (h *Handler) ListMyReferrals(ctx context.Context, req *plansv1.ListMyReferralsRequest) (*plansv1.ListMyReferralsResponse, error) {
	return h.qry.Referrals(ctx, req)
}

func (h *Handler) ListMyCommissions(ctx context.Context, req *plansv1.ListMyCommissionsRequest) (*plansv1.ListMyCommissionsResponse, error) {
	return h.qry.Commissions(ctx, req)
}

func (h *Handler) ListMyPayouts(ctx context.Context, req *plansv1.ListMyPayoutsRequest) (*plansv1.ListMyPayoutsResponse, error) {
	return h.qry.Payouts(ctx, req)
}
