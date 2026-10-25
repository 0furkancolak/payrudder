// Package command starts the reset of a sandbox (phase 45 E). The work is the runner's; this checks who asks, rate-limits, and queues the job.
package command

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	idquery "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands queues resets.
type Commands struct {
	pool     *pgxpool.Pool
	steps    []string
	cooldown time.Duration
	log      *slog.Logger
}

// New returns the commands. steps are the names of the runner's steps, shown as pending the moment the job is queued; cooldown is the least time between two manual
// resets of one sandbox.
func New(systemPool *pgxpool.Pool, steps []string, cooldown time.Duration, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, steps: steps, cooldown: cooldown, log: log}
}

// Request queues a reset of the sandbox of an app the user owns or administers. A reset already queued or running is the answer to asking again (nothing is
// queued twice); a second one right after the last is refused until the cooldown has passed.
func (c *Commands) Request(ctx context.Context, req *plansv1.ResetMySandboxRequest) (*plansv1.SandboxReset, error) {
	db := sqlc.New(c.pool)
	app, role, err := idquery.AppOf(ctx, db, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	if !idquery.CanManage(role) {
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account does not allow this")
	}
	sb, err := query.SandboxOf(app)
	if err != nil {
		return nil, err
	}
	tid, _ := uuid.Parse(sb.GetId())
	aid, _ := uuid.Parse(app.GetId())
	uid, _ := uuid.Parse(req.GetUserId())

	// The open one, if any, is what the person gets.
	if latest, err := db.LatestSandboxReset(ctx, tid); err == nil && (latest.State == sandbox.StateQueued || latest.State == sandbox.StateRunning) {
		return query.Message(latest), nil
	}
	recent, err := db.CountRecentManualResets(ctx, sqlc.CountRecentManualResetsParams{TenantID: tid, Within: convert.Interval(c.cooldown)})
	if err != nil {
		return nil, convert.Err(err, "sandbox reset")
	}
	if recent > 0 {
		return nil, errkey.Newf(codes.ResourceExhausted, "identity.sandbox.reset_rate_limited", "a sandbox can be reset once every %s", c.cooldown)
	}
	row, err := db.InsertSandboxReset(ctx, sqlc.InsertSandboxResetParams{ID: uuid.New(), AppID: aid, TenantID: tid, RequestedBy: &uid, Kind: "manual", Steps: sandbox.Initial(c.steps)})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		// Another request queued one between the look and the insert: that is the answer.
		if latest, lerr := db.LatestSandboxReset(ctx, tid); lerr == nil {
			return query.Message(latest), nil
		}
	}
	if err != nil {
		return nil, convert.Err(err, "sandbox reset")
	}
	c.log.InfoContext(ctx, "sandbox reset queued", "reset_id", row.ID, "app_id", aid, "tenant_id", tid, "user_id", uid)
	return query.Message(row), nil
}
