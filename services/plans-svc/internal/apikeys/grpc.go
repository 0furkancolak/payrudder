// Package apikeys is the module behind the API keys of AdminService: an operator makes and revokes them, the gateway resolves and touches them.
package apikeys

import (
	"context"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// Handler serves the API-key calls of AdminService.
type Handler struct {
	cmd *command.Commands
	qry *query.Queries
}

// New returns the API-key handlers.
func New(cmd *command.Commands, qry *query.Queries) *Handler { return &Handler{cmd: cmd, qry: qry} }

func (h *Handler) CreateApiKey(ctx context.Context, req *plansv1.CreateApiKeyRequest) (*plansv1.CreateApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	role, perms, ok := domain.KeyGrant(req.GetRole(), req.GetPermissions(), req.GetScope())
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "plans.api_key.bad_role", "role: admin, billing, read_only, ingest_only or custom, with known permissions")
	}
	key, plaintext, err := h.cmd.Issue(ctx, id, role, perms)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateApiKeyResponse{ApiKey: key, Plaintext: plaintext}, nil
}

func (h *Handler) RevokeApiKey(ctx context.Context, req *plansv1.RevokeApiKeyRequest) (*plansv1.RevokeApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := domain.ParseID("api_key_id", req.GetApiKeyId())
	if err != nil {
		return nil, err
	}
	key, err := h.cmd.Revoke(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.RevokeApiKeyResponse{ApiKey: key}, nil
}

func (h *Handler) TouchApiKey(ctx context.Context, req *plansv1.TouchApiKeyRequest) (*plansv1.TouchApiKeyResponse, error) {
	id, err := domain.ParseID("api_key_id", req.GetApiKeyId())
	if err != nil {
		return nil, err
	}
	if err := h.cmd.Touch(ctx, id); err != nil {
		return nil, err
	}
	return &plansv1.TouchApiKeyResponse{}, nil
}

func (h *Handler) ResolveApiKey(ctx context.Context, req *plansv1.ResolveApiKeyRequest) (*plansv1.ResolveApiKeyResponse, error) {
	return h.qry.Resolve(ctx, req.GetPrefix())
}
