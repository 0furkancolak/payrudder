package command

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// What app_audit records.
const (
	auditRenamed  = "renamed"
	auditArchived = "archived"
)

// RenameApp gives an app, and with it its sandbox and live tenants, a new name. An owner or an admin may. The slug stays: it is an address. Invoices already
// issued keep the name they were issued under; new ones carry the new one.
func (c *Commands) RenameApp(ctx context.Context, req *plansv1.RenameMyAppRequest) (*plansv1.App, error) {
	name, err := domain.ValidateAppName(req.GetName())
	if err != nil {
		return nil, err
	}
	var out *plansv1.App
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		app, err := c.manageableApp(ctx, q, req.GetUserId(), req.GetAppId())
		if err != nil {
			return err
		}
		id, _ := uuid.Parse(app.GetId())
		if _, err := q.LockApp(ctx, id); err != nil {
			return err
		}
		row, err := q.RenameApp(ctx, sqlc.RenameAppParams{ID: id, Name: name})
		if err != nil {
			return err // no row: the app was archived while this request waited, which reads as "not found"
		}
		if err := q.RenameAppTenants(ctx, sqlc.RenameAppTenantsParams{AppID: id, Name: name}); err != nil {
			return err
		}
		tenants, err := q.ListAppTenants(ctx, id)
		if err != nil {
			return err
		}
		out = convert.App(row, tenants)
		return c.auditApp(ctx, q, id, req.GetUserId(), auditRenamed, map[string]any{"from": app.GetName(), "to": name})
	})
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	c.log.InfoContext(ctx, "app renamed", "user_id", req.GetUserId(), "app_id", out.GetId())
	return out, nil
}

// DeleteApp removes an app from its account. Only an owner may. Nothing is erased: the app is archived. It leaves the account's lists and
// every route of a signed-in person, its API keys are revoked in the same transaction and evicted from the gateway cache after it, and no key can be made
// for it again, so nothing is ingested for it. Its tenants, customers, subscriptions, invoices, payments and usage stay, and the billing services keep
// working on what already exists: a customer who owes is still invoiced and charged, and an issued invoice can still be paid.
func (c *Commands) DeleteApp(ctx context.Context, req *plansv1.DeleteMyAppRequest) (*plansv1.DeleteMyAppResponse, error) {
	var (
		archived sqlc.App
		tenants  []uuid.UUID
	)
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		app, role, err := query.AppOf(ctx, q, req.GetUserId(), req.GetAppId())
		if err != nil {
			return err
		}
		// Deleting is the owner's: an admin manages the app but does not end it.
		if role != query.RoleOwner {
			return forbidden()
		}
		id, _ := uuid.Parse(app.GetId())
		uid, _ := uuid.Parse(req.GetUserId())
		if _, err := q.LockApp(ctx, id); err != nil {
			return err
		}
		archived, err = q.ArchiveApp(ctx, sqlc.ArchiveAppParams{ID: id, UserID: &uid})
		if err != nil {
			return err // no row: another request archived it first, which reads as "not found"
		}
		revoked, err := q.RevokeAppApiKeys(ctx, id)
		if err != nil {
			return err
		}
		for _, t := range app.GetTenants() {
			if tid, err := uuid.Parse(t.GetId()); err == nil {
				tenants = append(tenants, tid)
			}
		}
		return c.auditApp(ctx, q, id, req.GetUserId(), auditArchived, map[string]any{"revoked_keys": len(revoked)})
	})
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	// The revoked keys stop working at once, not when their entry in the gateway's cache ends.
	for _, tid := range tenants {
		if err := c.keys.EvictTenant(ctx, tid); err != nil {
			c.log.WarnContext(ctx, "archived app: gateway cache not evicted", "app_id", archived.ID, "tenant_id", tid, "err", err)
		}
	}
	c.log.WarnContext(ctx, "app deleted (archived)", "user_id", req.GetUserId(), "account_id", archived.AccountID, "app_id", archived.ID)
	return &plansv1.DeleteMyAppResponse{ArchiveTime: timestamppb.New(*archived.ArchivedAt)}, nil
}

// auditApp writes the audit row of a change to an app, in the transaction of the change.
func (c *Commands) auditApp(ctx context.Context, q *sqlc.Queries, appID uuid.UUID, userID, action string, detail map[string]any) error {
	uid, _ := uuid.Parse(userID)
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return q.InsertAppAudit(ctx, sqlc.InsertAppAuditParams{ID: idgen.NewUUIDv7(), AppID: appID, UserID: uid, Action: action, Detail: b})
}
