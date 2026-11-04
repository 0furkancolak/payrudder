package mailintegration

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/mailintegration/command"
)

// OrganisationHandler serves plansv1.OrganisationMailService: the e-mail accounts an organisation shares among its apps (ADR 0026).
type OrganisationHandler struct {
	plansv1.UnimplementedOrganisationMailServiceServer
	cmd *command.Commands
}

// NewOrganisation returns the organisation-level mail account handlers.
func NewOrganisation(cmd *command.Commands) *OrganisationHandler {
	return &OrganisationHandler{cmd: cmd}
}

func (h *OrganisationHandler) ListOrganisationMailAccounts(ctx context.Context, req *plansv1.ListOrganisationMailAccountsRequest) (*plansv1.ListOrganisationMailAccountsResponse, error) {
	accounts, canWrite, err := h.cmd.ListOrganisation(ctx, req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListOrganisationMailAccountsResponse{Accounts: accounts, CanWrite: canWrite}, nil
}

func (h *OrganisationHandler) PutOrganisationMailAccount(ctx context.Context, req *plansv1.PutOrganisationMailAccountRequest) (*plansv1.PutOrganisationMailAccountResponse, error) {
	a, err := h.cmd.PutOrganisation(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.PutOrganisationMailAccountResponse{Account: a}, nil
}

func (h *OrganisationHandler) DeleteOrganisationMailAccount(ctx context.Context, req *plansv1.DeleteOrganisationMailAccountRequest) (*plansv1.DeleteOrganisationMailAccountResponse, error) {
	if err := h.cmd.DeleteOrganisation(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.DeleteOrganisationMailAccountResponse{}, nil
}

func (h *OrganisationHandler) TestOrganisationMailAccount(ctx context.Context, req *plansv1.TestOrganisationMailAccountRequest) (*plansv1.TestOrganisationMailAccountResponse, error) {
	return h.cmd.TestOrganisation(ctx, req)
}

func (h *OrganisationHandler) BindOrganisationMailAccount(ctx context.Context, req *plansv1.BindOrganisationMailAccountRequest) (*plansv1.BindOrganisationMailAccountResponse, error) {
	if err := h.cmd.BindOrganisation(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.BindOrganisationMailAccountResponse{}, nil
}

func (h *OrganisationHandler) UnbindOrganisationMailAccount(ctx context.Context, req *plansv1.UnbindOrganisationMailAccountRequest) (*plansv1.UnbindOrganisationMailAccountResponse, error) {
	if err := h.cmd.UnbindOrganisation(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.UnbindOrganisationMailAccountResponse{}, nil
}
