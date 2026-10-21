// Package identity registers the handlers of IdentityService: request to command or query, result to response. Only the gateway calls it; it is not
// an operator service and it is not reachable by tenants. Account-scoped calls carry the user id the gateway resolved from the session, and the
// queries and commands find rows only through that user's membership.
package identity

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/query"
)

// Handler serves plansv1.IdentityService.
type Handler struct {
	plansv1.UnimplementedIdentityServiceServer
	cmd *command.Commands
	qry *query.Queries
}

// New returns the identity handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) SignUp(ctx context.Context, req *plansv1.SignUpRequest) (*plansv1.SignUpResponse, error) {
	o, err := h.cmd.SignUp(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SignUpResponse{User: o.User, Memberships: o.Memberships, Token: o.Token, ExpireTime: timestamppb.New(o.ExpireTime)}, nil
}

func (h *Handler) SignIn(ctx context.Context, req *plansv1.SignInRequest) (*plansv1.SignInResponse, error) {
	o, err := h.cmd.SignIn(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SignInResponse{User: o.User, Memberships: o.Memberships, Token: o.Token, ExpireTime: timestamppb.New(o.ExpireTime)}, nil
}

func (h *Handler) SignOut(ctx context.Context, req *plansv1.SignOutRequest) (*plansv1.SignOutResponse, error) {
	if err := h.cmd.SignOut(ctx, req.GetToken()); err != nil {
		return nil, err
	}
	return &plansv1.SignOutResponse{}, nil
}

func (h *Handler) GetSession(ctx context.Context, req *plansv1.GetSessionRequest) (*plansv1.GetSessionResponse, error) {
	s, err := h.qry.Session(ctx, req.GetToken())
	if err != nil {
		return nil, err
	}
	if s.Slide {
		h.cmd.Slide(ctx, s.ID, s.NewExpire)
	}
	return &plansv1.GetSessionResponse{User: s.User, Memberships: s.Memberships, ExpireTime: timestamppb.New(s.ExpireTime)}, nil
}

func (h *Handler) GetMyAccount(ctx context.Context, req *plansv1.GetMyAccountRequest) (*plansv1.GetMyAccountResponse, error) {
	return h.qry.Account(ctx, req.GetUserId(), req.GetAccountId())
}

func (h *Handler) CreateMyApp(ctx context.Context, req *plansv1.CreateMyAppRequest) (*plansv1.CreateMyAppResponse, error) {
	app, err := h.cmd.CreateApp(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateMyAppResponse{App: app}, nil
}

func (h *Handler) ListMyApps(ctx context.Context, req *plansv1.ListMyAppsRequest) (*plansv1.ListMyAppsResponse, error) {
	apps, err := h.qry.Apps(ctx, req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListMyAppsResponse{Apps: apps}, nil
}

func (h *Handler) GetMyApp(ctx context.Context, req *plansv1.GetMyAppRequest) (*plansv1.GetMyAppResponse, error) {
	app, role, err := h.qry.App(ctx, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetMyAppResponse{App: app, Role: role}, nil
}

func (h *Handler) ResolveMyTenant(ctx context.Context, req *plansv1.ResolveMyTenantRequest) (*plansv1.ResolveMyTenantResponse, error) {
	t, role, err := h.qry.Tenant(ctx, req.GetUserId(), req.GetTenantId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ResolveMyTenantResponse{Tenant: t, Role: role}, nil
}

func (h *Handler) CreateMyApiKey(ctx context.Context, req *plansv1.CreateMyApiKeyRequest) (*plansv1.CreateMyApiKeyResponse, error) {
	key, plaintext, err := h.cmd.CreateAPIKey(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateMyApiKeyResponse{ApiKey: key, Plaintext: plaintext}, nil
}

func (h *Handler) ListMyApiKeys(ctx context.Context, req *plansv1.ListMyApiKeysRequest) (*plansv1.ListMyApiKeysResponse, error) {
	keys, err := h.qry.APIKeys(ctx, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListMyApiKeysResponse{ApiKeys: keys}, nil
}

func (h *Handler) RevokeMyApiKey(ctx context.Context, req *plansv1.RevokeMyApiKeyRequest) (*plansv1.RevokeMyApiKeyResponse, error) {
	key, err := h.cmd.RevokeAPIKey(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.RevokeMyApiKeyResponse{ApiKey: key}, nil
}
