// Package mailintegration registers the handlers of MailIntegrationService: request to command or query, result to response.
package mailintegration

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/mailintegration/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/mailintegration/query"
)

// Handler serves plansv1.MailIntegrationService.
type Handler struct {
	plansv1.UnimplementedMailIntegrationServiceServer
	cmd *command.Commands
	qry *query.Queries
}

// New returns the mail integration handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) GetMailIntegration(ctx context.Context, _ *plansv1.GetMailIntegrationRequest) (*plansv1.GetMailIntegrationResponse, error) {
	all, err := h.qry.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &plansv1.GetMailIntegrationResponse{Connected: len(all) > 0, Accounts: all}
	if len(all) > 0 {
		out.Integration = all[0] // the default is first
	}
	return out, nil
}

func (h *Handler) PutMailIntegration(ctx context.Context, req *plansv1.PutMailIntegrationRequest) (*plansv1.PutMailIntegrationResponse, error) {
	m, err := h.cmd.Put(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.PutMailIntegrationResponse{Integration: m}, nil
}

func (h *Handler) DeleteMailIntegration(ctx context.Context, req *plansv1.DeleteMailIntegrationRequest) (*plansv1.DeleteMailIntegrationResponse, error) {
	if err := h.cmd.Delete(ctx, req.GetAccount()); err != nil {
		return nil, err
	}
	return &plansv1.DeleteMailIntegrationResponse{}, nil
}

func (h *Handler) TestMailIntegration(ctx context.Context, req *plansv1.TestMailIntegrationRequest) (*plansv1.TestMailIntegrationResponse, error) {
	return h.cmd.Test(ctx, req.GetTo(), req.GetAccount())
}

func (h *Handler) ResolveMailSender(ctx context.Context, req *plansv1.ResolveMailSenderRequest) (*plansv1.ResolveMailSenderResponse, error) {
	s, err := h.qry.ResolveSender(ctx, req.GetLanguage())
	if err != nil {
		return nil, err
	}
	if s == nil {
		return &plansv1.ResolveMailSenderResponse{}, nil
	}
	return &plansv1.ResolveMailSenderResponse{Connected: true, Provider: s.Provider, BaseUrl: s.BaseURL, ApiKey: s.APIKey, From: s.From, Account: s.Account}, nil
}

func (h *Handler) SetDefaultMailIntegration(ctx context.Context, req *plansv1.SetDefaultMailIntegrationRequest) (*plansv1.SetDefaultMailIntegrationResponse, error) {
	if err := h.cmd.SetDefault(ctx, req.GetAccount()); err != nil {
		return nil, err
	}
	return &plansv1.SetDefaultMailIntegrationResponse{}, nil
}

func (h *Handler) GetMailRouting(ctx context.Context, _ *plansv1.GetMailRoutingRequest) (*plansv1.GetMailRoutingResponse, error) {
	rules, err := h.qry.Routing(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMailRoutingResponse{Rules: rules}, nil
}

func (h *Handler) PutMailRouting(ctx context.Context, req *plansv1.PutMailRoutingRequest) (*plansv1.PutMailRoutingResponse, error) {
	rules, err := h.cmd.SetRouting(ctx, req.GetRules())
	if err != nil {
		return nil, err
	}
	return &plansv1.PutMailRoutingResponse{Rules: rules}, nil
}
