// Package identity registers the handlers of IdentityService: request to command or query, result to response. Only the gateway calls it; it is not
// an operator service and it is not reachable by tenants. Account-scoped calls carry the user id the gateway resolved from the session, and the
// queries and commands find rows only through that user's membership.
package identity

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/golive"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/command"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/query"
)

// Handler serves plansv1.IdentityService.
type Handler struct {
	plansv1.UnimplementedIdentityServiceServer
	cmd    *command.Commands
	qry    *query.Queries
	goLive *golive.Handler
}

// New returns the identity handlers. goLive serves the two go-live calls (phase 45).
func New(cmd *command.Commands, qry *query.Queries, goLive *golive.Handler) *Handler {
	return &Handler{cmd: cmd, qry: qry, goLive: goLive}
}

func (h *Handler) GetMyGoLive(ctx context.Context, req *plansv1.GetMyGoLiveRequest) (*plansv1.GetMyGoLiveResponse, error) {
	return h.goLive.GetMyGoLive(ctx, req)
}

func (h *Handler) ActivateMyApp(ctx context.Context, req *plansv1.ActivateMyAppRequest) (*plansv1.ActivateMyAppResponse, error) {
	return h.goLive.ActivateMyApp(ctx, req)
}

func (h *Handler) RotateMyApiKey(ctx context.Context, req *plansv1.RotateMyApiKeyRequest) (*plansv1.RotateMyApiKeyResponse, error) {
	fresh, plaintext, previous, err := h.cmd.RotateAPIKey(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.RotateMyApiKeyResponse{ApiKey: fresh, Plaintext: plaintext, Previous: previous}, nil
}

func (h *Handler) SignUp(ctx context.Context, req *plansv1.SignUpRequest) (*plansv1.SignUpResponse, error) {
	if err := h.cmd.SignUp(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.SignUpResponse{}, nil
}

func (h *Handler) VerifyEmail(ctx context.Context, req *plansv1.VerifyEmailRequest) (*plansv1.VerifyEmailResponse, error) {
	u, err := h.cmd.VerifyEmail(ctx, req.GetToken())
	if err != nil {
		return nil, err
	}
	return &plansv1.VerifyEmailResponse{User: u}, nil
}

func (h *Handler) ResendVerification(ctx context.Context, req *plansv1.ResendVerificationRequest) (*plansv1.ResendVerificationResponse, error) {
	if err := h.cmd.ResendVerification(ctx, req.GetUserId()); err != nil {
		return nil, err
	}
	return &plansv1.ResendVerificationResponse{}, nil
}

func (h *Handler) RequestPasswordReset(ctx context.Context, req *plansv1.RequestPasswordResetRequest) (*plansv1.RequestPasswordResetResponse, error) {
	if err := h.cmd.RequestPasswordReset(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.RequestPasswordResetResponse{}, nil
}

func (h *Handler) ResetPassword(ctx context.Context, req *plansv1.ResetPasswordRequest) (*plansv1.ResetPasswordResponse, error) {
	if err := h.cmd.ResetPassword(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.ResetPasswordResponse{}, nil
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

func (h *Handler) RequestLoginCode(ctx context.Context, req *plansv1.RequestLoginCodeRequest) (*plansv1.RequestLoginCodeResponse, error) {
	if err := h.cmd.RequestLoginCode(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.RequestLoginCodeResponse{}, nil
}

func (h *Handler) VerifyLoginCode(ctx context.Context, req *plansv1.VerifyLoginCodeRequest) (*plansv1.VerifyLoginCodeResponse, error) {
	o, err := h.cmd.VerifyLoginCode(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.VerifyLoginCodeResponse{User: o.User, Memberships: o.Memberships, Token: o.Token, ExpireTime: timestamppb.New(o.ExpireTime)}, nil
}

func (h *Handler) SignInWithIdentity(ctx context.Context, req *plansv1.SignInWithIdentityRequest) (*plansv1.SignInWithIdentityResponse, error) {
	o, err := h.cmd.SignInWithIdentity(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.SignInWithIdentityResponse{User: o.User, Memberships: o.Memberships, Token: o.Token, ExpireTime: timestamppb.New(o.ExpireTime)}, nil
}

func (h *Handler) RenameMyAccount(ctx context.Context, req *plansv1.RenameMyAccountRequest) (*plansv1.RenameMyAccountResponse, error) {
	name, err := h.cmd.RenameAccount(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.RenameMyAccountResponse{Name: name}, nil
}

func (h *Handler) InviteMember(ctx context.Context, req *plansv1.InviteMemberRequest) (*plansv1.InviteMemberResponse, error) {
	inv, err := h.cmd.InviteMember(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.InviteMemberResponse{Invitation: inv}, nil
}

func (h *Handler) ListInvitations(ctx context.Context, req *plansv1.ListInvitationsRequest) (*plansv1.ListInvitationsResponse, error) {
	list, err := h.qry.Invitations(ctx, req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListInvitationsResponse{Invitations: list}, nil
}

func (h *Handler) RevokeInvitation(ctx context.Context, req *plansv1.RevokeInvitationRequest) (*plansv1.RevokeInvitationResponse, error) {
	if err := h.cmd.RevokeInvitation(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.RevokeInvitationResponse{}, nil
}

func (h *Handler) ResendInvitation(ctx context.Context, req *plansv1.ResendInvitationRequest) (*plansv1.ResendInvitationResponse, error) {
	inv, err := h.cmd.ResendInvitation(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.ResendInvitationResponse{Invitation: inv}, nil
}

func (h *Handler) UpdateMemberRole(ctx context.Context, req *plansv1.UpdateMemberRoleRequest) (*plansv1.UpdateMemberRoleResponse, error) {
	role, err := h.cmd.UpdateMemberRole(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.UpdateMemberRoleResponse{Role: role}, nil
}

func (h *Handler) RemoveMember(ctx context.Context, req *plansv1.RemoveMemberRequest) (*plansv1.RemoveMemberResponse, error) {
	if err := h.cmd.RemoveMember(ctx, req); err != nil {
		return nil, err
	}
	return &plansv1.RemoveMemberResponse{}, nil
}

func (h *Handler) LookupInvitation(ctx context.Context, req *plansv1.LookupInvitationRequest) (*plansv1.LookupInvitationResponse, error) {
	return h.cmd.LookupInvitation(ctx, req.GetToken())
}

func (h *Handler) AcceptInvitation(ctx context.Context, req *plansv1.AcceptInvitationRequest) (*plansv1.AcceptInvitationResponse, error) {
	o, err := h.cmd.AcceptInvitation(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.AcceptInvitationResponse{User: o.User, Memberships: o.Memberships, Token: o.Token, ExpireTime: timestamppb.New(o.ExpireTime)}, nil
}

func (h *Handler) AcceptInvitationAsUser(ctx context.Context, req *plansv1.AcceptInvitationAsUserRequest) (*plansv1.AcceptInvitationAsUserResponse, error) {
	return h.cmd.AcceptInvitationAsUser(ctx, req)
}
