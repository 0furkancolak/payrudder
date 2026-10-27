// Package command activates a live environment (phase 45). Activation is the one way a live tenant leaves `inactive`: the owner asks, the checklist is computed
// again inside the transaction that decides, and only a complete one passes. An operator has a separate hand (AdminService.SetTenantStatus).
package command

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/query"
	idquery "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes the life of a live tenant.
type Commands struct {
	pool   *pgxpool.Pool
	places *placement.Places
	log    *slog.Logger
}

// New returns the go-live commands on the system pool; places says where the live tenant's own rows (its plans) are.
func New(places *placement.Places, log *slog.Logger) *Commands {
	return &Commands{pool: places.Pool(), places: places, log: log}
}

// The actions of the go-live audit trail (go_live_audit.action).
const (
	auditBillingProfile = "billing_profile_saved"
	auditTerms          = "terms_accepted"
)

// ownerLive finds the live tenant of an app the user is the owner of and locks its row, inside tx. Anyone else is refused: a member who is not the owner
// is told so, and an app that is not the user's is not found.
func ownerLive(ctx context.Context, q *sqlc.Queries, userID, appID string) (app *plansv1.App, row sqlc.Tenant, err error) {
	app, role, err := idquery.AppOf(ctx, q, userID, appID)
	if err != nil {
		return nil, row, err
	}
	if role != idquery.RoleOwner {
		return nil, row, errkey.New(codes.PermissionDenied, "identity.go_live.owner_only", "only an owner of the account can prepare the live environment")
	}
	liveMsg, err := query.LiveTenant(app)
	if err != nil {
		return nil, row, err
	}
	tid, err := uuid.Parse(liveMsg.GetId())
	if err != nil {
		return nil, row, idquery.NotFound("tenant")
	}
	row, err = q.LockTenantForGoLive(ctx, tid)
	if err != nil {
		return nil, row, convert.Err(err, "tenant")
	}
	return app, row, nil
}

func audit(ctx context.Context, q *sqlc.Queries, app *plansv1.App, tenant uuid.UUID, userID, action string, detail map[string]any) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return idquery.NotFound("app")
	}
	aid, err := uuid.Parse(app.GetId())
	if err != nil {
		return idquery.NotFound("app")
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return q.InsertGoLiveAudit(ctx, sqlc.InsertGoLiveAuditParams{ID: uuid.New(), AppID: aid, LiveTenantID: tenant, UserID: uid, Action: action, Detail: raw})
}

// UpdateBillingProfile saves the seller's legal name, tax id and address on the app's live tenant. Only the owner may; the profile is checked as the checklist
// reads it (and the address lines an invoice prints), and the change is written to the audit trail in the same transaction. The trail holds the country and
// whether a tax id was given, never the profile itself.
func (c *Commands) UpdateBillingProfile(ctx context.Context, req *plansv1.UpdateMyBillingProfileRequest) (*plansv1.Tenant, error) {
	var out *plansv1.Tenant
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		app, row, err := ownerLive(ctx, q, req.GetUserId(), req.GetAppId())
		if err != nil {
			return err
		}
		address, legal := domain.NormalizeBillingProfile(req.GetAddress(), req.GetLegal())
		if err := domain.ValidateBillingProfile(address, legal); err != nil {
			return err
		}
		row, err = q.UpdateTenantBillingProfile(ctx, sqlc.UpdateTenantBillingProfileParams{ID: row.ID, Address: convert.AddressJSON(address), Legal: convert.LegalJSON(legal)})
		if err != nil {
			return convert.Err(err, "tenant")
		}
		if err := audit(ctx, q, app, row.ID, req.GetUserId(), auditBillingProfile, map[string]any{"country": address.GetCountry(), "tax_id_set": legal.GetTaxId() != ""}); err != nil {
			return err
		}
		out = convert.Tenant(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	c.log.InfoContext(ctx, "billing profile saved", "user_id", req.GetUserId(), "app_id", req.GetAppId(), "tenant_id", out.GetId())
	return out, nil
}

// AcceptTerms records the owner's acceptance of the terms of service for the live environment. The first acceptance is kept; asking again changes nothing
// and writes nothing.
func (c *Commands) AcceptTerms(ctx context.Context, req *plansv1.AcceptMyTermsRequest) (*timestamppb.Timestamp, error) {
	var out *timestamppb.Timestamp
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		app, row, err := ownerLive(ctx, q, req.GetUserId(), req.GetAppId())
		if err != nil {
			return err
		}
		if row.TermsAcceptedAt == nil {
			if err := acceptTerms(ctx, q, app, row.ID, req.GetUserId()); err != nil {
				return err
			}
			if row, err = q.LockTenantForGoLive(ctx, row.ID); err != nil {
				return err
			}
		}
		out = convert.Tenant(row).GetTermsAcceptTime()
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return out, nil
}

// acceptTerms writes the acceptance and its audit row.
func acceptTerms(ctx context.Context, q *sqlc.Queries, app *plansv1.App, tenant uuid.UUID, userID string) error {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return idquery.NotFound("app")
	}
	if err := q.AcceptTenantTerms(ctx, sqlc.AcceptTenantTermsParams{ID: tenant, UserID: &uid}); err != nil {
		return err
	}
	return audit(ctx, q, app, tenant, userID, auditTerms, map[string]any{})
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
			out, err = query.Checklist(ctx, tx, c.places, convert.Tenant(row), req.GetProviderReady())
			return err
		case domain.StatusSuspended:
			return errkey.New(codes.FailedPrecondition, "identity.go_live.suspended", "this environment is suspended: contact support")
		}
		if req.GetAcceptTerms() {
			if row.TermsAcceptedAt == nil {
				if err := acceptTerms(ctx, q, app, tid, req.GetUserId()); err != nil {
					return err
				}
			}
			if row, err = q.LockTenantForGoLive(ctx, tid); err != nil {
				return err
			}
		}
		list, err := query.Checklist(ctx, tx, c.places, convert.Tenant(row), req.GetProviderReady())
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
