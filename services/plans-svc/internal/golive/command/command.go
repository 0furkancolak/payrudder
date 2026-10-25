// Package command activates a live environment (phase 45). Activation is the one way a live tenant leaves `inactive`: the owner asks, the checklist is computed
// again inside the transaction that decides, and only a complete one passes. An operator has a separate hand (AdminService.SetTenantStatus).
package command

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/golive/query"
	idquery "github.com/0furkancolak/quotify/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Commands changes the life of a live tenant.
type Commands struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// New returns the go-live commands on the system pool.
func New(systemPool *pgxpool.Pool, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, log: log}
}

// Activate turns the live tenant of an app the owner belongs to from inactive to active. It is idempotent: an active tenant answers with its checklist.
func (c *Commands) Activate(ctx context.Context, req *plansv1.ActivateMyAppRequest) (*plansv1.GoLive, error) {
	var out *plansv1.GoLive
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		app, role, err := idquery.AppOf(ctx, q, req.GetUserId(), req.GetAppId())
		if err != nil {
			return err
		}
		if role != idquery.RoleOwner {
			return errkey.New(codes.PermissionDenied, "identity.go_live.owner_only", "only an owner of the account can activate the live environment")
		}
		liveMsg, err := query.LiveTenant(app)
		if err != nil {
			return err
		}
		tid, err := uuid.Parse(liveMsg.GetId())
		if err != nil {
			return idquery.NotFound("tenant")
		}
		// One activation at a time per tenant: the checklist below is what this transaction decides on.
		row, err := q.LockTenantForGoLive(ctx, tid)
		if err != nil {
			return convert.Err(err, "tenant")
		}
		switch row.Status {
		case domain.StatusActive:
			out, err = query.Checklist(ctx, q, convert.Tenant(row), req.GetProviderReady())
			return err
		case domain.StatusSuspended:
			return errkey.New(codes.FailedPrecondition, "identity.go_live.suspended", "this environment is suspended: contact support")
		}
		if req.GetAcceptTerms() {
			uid, _ := uuid.Parse(req.GetUserId())
			if err := q.AcceptTenantTerms(ctx, sqlc.AcceptTenantTermsParams{ID: tid, UserID: &uid}); err != nil {
				return err
			}
			if row, err = q.LockTenantForGoLive(ctx, tid); err != nil {
				return err
			}
		}
		list, err := query.Checklist(ctx, q, convert.Tenant(row), req.GetProviderReady())
		if err != nil {
			return err
		}
		if !list.GetReady() {
			return errkey.Newf(codes.FailedPrecondition, "identity.go_live.incomplete", "the go-live checklist is not complete: %s", query.Missing(list))
		}
		row, err = q.SetTenantStatus(ctx, sqlc.SetTenantStatusParams{ID: tid, Status: domain.StatusActive})
		if err != nil {
			return err
		}
		out = list
		out.Status = domain.StatusFromDB(row.Status)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	if out.GetStatus() == plansv1.TenantStatus_TENANT_STATUS_ACTIVE {
		c.log.InfoContext(ctx, "live environment activated", "user_id", req.GetUserId(), "app_id", req.GetAppId(), "tenant_id", out.GetTenantId())
	}
	return out, nil
}
