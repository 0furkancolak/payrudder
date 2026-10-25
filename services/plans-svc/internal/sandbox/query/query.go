// Package query reads the resets of an app's sandbox. It never writes.
package query

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	idquery "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads on the system pool.
type Queries struct{ pool *pgxpool.Pool }

// New returns the sandbox reset queries.
func New(systemPool *pgxpool.Pool) *Queries { return &Queries{pool: systemPool} }

// Message is the API shape of a job row.
func Message(r sqlc.SandboxReset) *plansv1.SandboxReset {
	out := &plansv1.SandboxReset{Id: r.ID.String(), State: r.State, Kind: r.Kind, Error: r.Error, CreateTime: timestamppb.New(r.CreatedAt)}
	if r.FinishedAt != nil {
		out.FinishTime = timestamppb.New(*r.FinishedAt)
	}
	var steps []struct {
		Name, State, Error string
		Deleted            map[string]int64
	}
	if json.Unmarshal(r.Steps, &steps) == nil {
		for _, s := range steps {
			out.Steps = append(out.Steps, &plansv1.SandboxResetStep{Name: s.Name, State: s.State, Error: s.Error, Deleted: s.Deleted})
		}
	}
	return out
}

// SandboxOf finds the sandbox tenant of an app of the user's account.
func SandboxOf(app *plansv1.App) (*plansv1.Tenant, error) {
	for _, t := range app.GetTenants() {
		if t.GetEnvironment() == env.Sandbox.Proto() {
			return t, nil
		}
	}
	return nil, idquery.NotFound("tenant")
}

// Latest returns the latest reset of the app's sandbox, or nil when there has been none. Any member of the account may look.
func (q *Queries) Latest(ctx context.Context, userID, appID string) (*plansv1.SandboxReset, error) {
	db := sqlc.New(q.pool)
	app, _, err := idquery.AppOf(ctx, db, userID, appID)
	if err != nil {
		return nil, err
	}
	sb, err := SandboxOf(app)
	if err != nil {
		return nil, err
	}
	id, ok := idquery.ParseID(sb.GetId())
	if !ok {
		return nil, idquery.NotFound("tenant")
	}
	row, err := db.LatestSandboxReset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, convert.Err(err, "sandbox reset")
	}
	return Message(row), nil
}
