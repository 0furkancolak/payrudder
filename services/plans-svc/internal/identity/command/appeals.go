package command

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// liveTenantOf is the live environment of an app (nil when it has none).
func liveTenantOf(app *plansv1.App) *plansv1.Tenant {
	for _, t := range app.GetTenants() {
		if t.GetEnvironment() == env.Live.Proto() {
			return t
		}
	}
	return nil
}

// held says whether the operator has taken a live tenant out of service.
func held(s plansv1.TenantStatus) (string, bool) {
	word, _ := domain.StatusToDB(s)
	return word, word == domain.StatusSuspended || word == domain.StatusFrozen || word == domain.StatusBanned
}

// AppHold says what the operator has done to an app's live environment as its owners may know it (plan 14): the status, a suspension or freeze scheduled for it, the date a
// banned app's data is kept until, and the latest appeal. Any member may read it. The operator's category and note are not in it.
func (c *Commands) AppHold(ctx context.Context, req *plansv1.GetMyAppHoldRequest) (*plansv1.GetMyAppHoldResponse, error) {
	q := sqlc.New(c.pool)
	app, role, err := query.AppOf(ctx, q, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	live := liveTenantOf(app)
	if live == nil {
		return &plansv1.GetMyAppHoldResponse{}, nil
	}
	id, _ := uuid.Parse(live.GetId())
	out := &plansv1.GetMyAppHoldResponse{Status: live.GetStatus()}
	if l, err := q.GetTenantLifecycle(ctx, id); err == nil {
		if l.ScheduledStatus != nil && l.ScheduledAt != nil {
			out.ScheduledStatus, out.ScheduledTime = *l.ScheduledStatus, timestamppb.New(*l.ScheduledAt)
		}
		if l.RetentionUntil != nil && live.GetStatus() == plansv1.TenantStatus_TENANT_STATUS_BANNED {
			out.RetentionUntil = timestamppb.New(*l.RetentionUntil)
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, convert.Err(err, "app")
	}
	open := false
	if a, err := q.LatestTenantAppeal(ctx, id); err == nil {
		out.Appeal = convert.TenantAppeal(a)
		open = a.Status == "open"
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, convert.Err(err, "app")
	}
	_, isHeld := held(live.GetStatus())
	out.CanAppeal = role == query.RoleOwner && isHeld && !open
	return out, nil
}

// SubmitAppeal records an owner's statement against a suspension, a freeze or a ban of the app's live environment. Only an owner may, only while the app is held, and one
// appeal is open at a time. The operator decides it (AdminService.DecideTenantAppeal).
func (c *Commands) SubmitAppeal(ctx context.Context, req *plansv1.SubmitMyAppAppealRequest) (*plansv1.TenantAppeal, error) {
	statement := strings.TrimSpace(req.GetStatement())
	if statement == "" || utf8.RuneCountInString(statement) > 2000 {
		return nil, domain.Invalid("statement", "required, at most 2000 characters")
	}
	var out sqlc.TenantAppeal
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		app, role, err := query.AppOf(ctx, q, req.GetUserId(), req.GetAppId())
		if err != nil {
			return err
		}
		if role != query.RoleOwner {
			return forbidden()
		}
		live := liveTenantOf(app)
		word, isHeld := "", false
		if live != nil {
			word, isHeld = held(live.GetStatus())
		}
		if !isHeld {
			return errkey.New(codes.FailedPrecondition, "plans.appeal.not_held", "only an app the operator has suspended, frozen or banned can be appealed")
		}
		id, _ := uuid.Parse(live.GetId())
		if a, err := q.LatestTenantAppeal(ctx, id); err == nil && a.Status == "open" {
			return errkey.New(codes.FailedPrecondition, "plans.appeal.exists", "an appeal is open already: the operator will answer it")
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		uid, _ := uuid.Parse(req.GetUserId())
		user, err := q.GetUser(ctx, uid)
		if err != nil {
			return err
		}
		out, err = q.InsertTenantAppeal(ctx, sqlc.InsertTenantAppealParams{ID: idgen.NewUUIDv7(), LiveTenantID: id, HeldStatus: word, Statement: statement, SubmittedBy: strings.ToLower(user.Email)})
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "tenant_appeal.submitted", TargetType: "tenant_appeal", TargetID: out.ID.String(),
			Summary: "appealed against the status " + word, Detail: map[string]any{"held_status": word}})
	})
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	return convert.TenantAppeal(out), nil
}
