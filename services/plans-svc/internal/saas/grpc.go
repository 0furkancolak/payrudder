// Package saas serves SaasService (phase 55, ADR 0024): the plan page of an organisation, the operator's view of all of them and the ingest policy the gateway
// meters and limits events by. The rules are in saas/command, the reads in saas/query and the pure rules in saas/model; this file maps messages.
package saas

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	identityqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Handler implements plansv1.SaasServiceServer.
type Handler struct {
	plansv1.UnimplementedSaasServiceServer
	cmd  *command.Commands
	qry  *query.Queries
	pool *pgxpool.Pool
}

// New returns the handler. pool is the system pool.
func New(cmd *command.Commands, qry *query.Queries, pool *pgxpool.Pool) *Handler {
	return &Handler{cmd: cmd, qry: qry, pool: pool}
}

// MeterCode is the platform-tenant meter the gateway counts accepted events on.
const MeterCode = "events"

// GetPlatform tells whether the mode is on and which tenant is the platform's.
func (h *Handler) GetPlatform(ctx context.Context, _ *plansv1.GetPlatformRequest) (*plansv1.GetPlatformResponse, error) {
	if !h.cmd.Enabled() {
		return &plansv1.GetPlatformResponse{}, nil
	}
	p, err := h.qry.Platform(ctx)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetPlatformResponse{Enabled: true, Tenant: p.Proto()}, nil
}

// member loads the caller's membership of the account. A partner of the account's affiliate program is not a member of the organisation.
func (h *Handler) member(ctx context.Context, userID, accountID string) (uuid.UUID, string, error) {
	_, aid, m, err := identityqry.Membership(ctx, sqlc.New(h.pool), userID, accountID)
	if err != nil {
		return uuid.Nil, "", err
	}
	if m.Role == identityqry.RoleAffiliate {
		return uuid.Nil, "", identityqry.NotFound("account")
	}
	return aid, m.Role, nil
}

func (h *Handler) view(ctx context.Context, account uuid.UUID, canManage bool) (*plansv1.OrganizationPlan, error) {
	if err := h.cmd.Ensure(ctx, account); err != nil {
		return nil, err
	}
	v, err := h.qry.View(ctx, account)
	if err != nil {
		return nil, err
	}
	v.CanManage = canManage
	return v, nil
}

// GetMyPlan is the plan page of an account the caller belongs to.
func (h *Handler) GetMyPlan(ctx context.Context, req *plansv1.GetMyPlanRequest) (*plansv1.GetMyPlanResponse, error) {
	aid, role, err := h.member(ctx, req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if !h.cmd.Enabled() {
		return &plansv1.GetMyPlanResponse{Plan: &plansv1.OrganizationPlan{AccountId: aid.String()}}, nil
	}
	v, err := h.view(ctx, aid, identityqry.CanManage(role))
	return &plansv1.GetMyPlanResponse{Plan: v}, err
}

// ChangeMyPlan moves the account to another plan.
func (h *Handler) ChangeMyPlan(ctx context.Context, req *plansv1.ChangeMyPlanRequest) (*plansv1.ChangeMyPlanResponse, error) {
	aid, role, err := h.member(ctx, req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if !identityqry.CanManage(role) {
		return nil, identityqry.Forbidden()
	}
	if err := h.cmd.ChangePlan(ctx, aid, req.GetPlanCode(), false); err != nil {
		return nil, err
	}
	v, err := h.view(ctx, aid, true)
	return &plansv1.ChangeMyPlanResponse{Plan: v}, err
}

// ResolveIngestPolicy says whether the events of a tenant are metered, and where.
func (h *Handler) ResolveIngestPolicy(ctx context.Context, req *plansv1.ResolveIngestPolicyRequest) (*plansv1.ResolveIngestPolicyResponse, error) {
	if !h.cmd.Enabled() {
		return &plansv1.ResolveIngestPolicyResponse{}, nil
	}
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, domain.Invalid("tenant_id", "must be a UUID")
	}
	row, err := sqlc.New(h.pool).SaasTenantAccount(ctx, tid)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	// The platform's own tenant is not metered (it would meter itself), and a sandbox is a place to try things: its events are free.
	if row.Platform || row.Environment != "live" {
		return &plansv1.ResolveIngestPolicyResponse{AccountId: row.AccountID.String()}, nil
	}
	if err := h.cmd.Ensure(ctx, row.AccountID); err != nil {
		return nil, err
	}
	p, err := h.qry.Platform(ctx)
	if err != nil {
		return nil, err
	}
	cust, err := sqlc.New(h.pool).SaasGetCustomer(ctx, sqlc.SaasGetCustomerParams{TenantID: p.TenantID, AccountID: row.AccountID.String()})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return &plansv1.ResolveIngestPolicyResponse{Metered: true, AccountId: row.AccountID.String(), Platform: p.Proto(), CustomerId: cust.ID.String(),
		CustomerExternalId: cust.ExternalID, MeterCode: MeterCode}, nil
}

// requireOperator refuses a caller the gateway did not mark as the operator, and answers a disabled mode.
func (h *Handler) requireOperator(ctx context.Context) error {
	if err := tenant.RequireOperator(ctx); err != nil {
		return err
	}
	if !h.cmd.Enabled() {
		return query.Disabled()
	}
	return nil
}

const (
	defaultPage = 50
	maxPage     = 200
)

// ListOrganizations is the operator's list.
func (h *Handler) ListOrganizations(ctx context.Context, req *plansv1.ListOrganizationsRequest) (*plansv1.ListOrganizationsResponse, error) {
	if err := h.requireOperator(ctx); err != nil {
		return nil, err
	}
	size := int(req.GetPageSize())
	if size <= 0 {
		size = defaultPage
	}
	size = min(size, maxPage)
	after := uuid.Nil
	if tok := req.GetPageToken(); tok != "" {
		id, err := uuid.Parse(tok)
		if err != nil {
			return nil, domain.Invalid("page_token", "not one this list gave")
		}
		after = id
	}
	rows, err := h.qry.List(ctx, after, req.GetQuery(), size+1)
	if err != nil {
		return nil, err
	}
	p, err := h.qry.Platform(ctx)
	if err != nil {
		return nil, err
	}
	out := &plansv1.ListOrganizationsResponse{Platform: p.Proto()}
	if len(rows) > size {
		rows = rows[:size]
		out.NextPageToken = rows[size-1].AccountID.String()
	}
	for _, r := range rows {
		out.Organizations = append(out.Organizations, r.Proto(time.Now()))
	}
	return out, nil
}

// account parses and checks an account id of an operator call.
func (h *Handler) account(ctx context.Context, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.Invalid("account_id", "must be a UUID")
	}
	a, err := sqlc.New(h.pool).SaasGetAccount(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, identityqry.NotFound("account")
		}
		return uuid.Nil, convert.Err(err, "account")
	}
	if a.Platform {
		return uuid.Nil, errkey.New(codes.InvalidArgument, "plans.saas.platform_account", "the platform's own account has no plan")
	}
	return id, nil
}

// SetOrganizationPlan is the operator's manual override.
func (h *Handler) SetOrganizationPlan(ctx context.Context, req *plansv1.SetOrganizationPlanRequest) (*plansv1.SetOrganizationPlanResponse, error) {
	if err := h.requireOperator(ctx); err != nil {
		return nil, err
	}
	aid, err := h.account(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if req.GetPlanCode() == "" && len(req.GetLimits()) == 0 {
		return nil, domain.Invalid("plan_code", "name a plan, limits, or both")
	}
	if code := req.GetPlanCode(); code != "" {
		if err := h.cmd.ChangePlan(ctx, aid, code, true); err != nil {
			return nil, err
		}
	}
	if len(req.GetLimits()) > 0 {
		if err := h.cmd.SetLimits(ctx, aid, req.GetLimits()); err != nil {
			return nil, err
		}
	}
	v, err := h.view(ctx, aid, true)
	return &plansv1.SetOrganizationPlanResponse{Plan: v}, err
}

// ClearOrganizationOverrides drops the organisation's own limits.
func (h *Handler) ClearOrganizationOverrides(ctx context.Context, req *plansv1.ClearOrganizationOverridesRequest) (*plansv1.ClearOrganizationOverridesResponse, error) {
	if err := h.requireOperator(ctx); err != nil {
		return nil, err
	}
	aid, err := h.account(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ClearOrganizationOverridesResponse{}, h.cmd.ClearLimits(ctx, aid)
}
