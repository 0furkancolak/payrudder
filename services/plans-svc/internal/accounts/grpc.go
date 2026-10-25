// Package accounts is the module behind the accounts and apps of AdminService: an operator creates an account with its first app, adds apps, lists them.
package accounts

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/accounts/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/accounts/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// Handler serves the account and app calls of AdminService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the account handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateAccount(ctx context.Context, req *plansv1.CreateAccountRequest) (*plansv1.CreateAccountResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	acct, err := h.cmd.CreateAccount(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateAccountResponse{Account: acct}, nil
}

func (h *Handler) CreateApp(ctx context.Context, req *plansv1.CreateAppRequest) (*plansv1.CreateAppResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	app, err := h.cmd.CreateApp(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateAppResponse{App: app}, nil
}

func (h *Handler) ListApps(ctx context.Context, req *plansv1.ListAppsRequest) (*plansv1.ListAppsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	accountID, err := domain.ParseID("account_id", req.GetAccountId())
	if err != nil {
		return nil, err
	}
	apps, err := h.qry.Apps(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return &plansv1.ListAppsResponse{Apps: apps}, nil
}
