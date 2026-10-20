// Package query reads provider migrations.
package query

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

var migrationStates = map[string]plansv1.ProviderMigration_State{
	"started":   plansv1.ProviderMigration_STATE_STARTED,
	"completed": plansv1.ProviderMigration_STATE_COMPLETED,
	"aborted":   plansv1.ProviderMigration_STATE_ABORTED,
}

// Message maps a migration row to its API message.
func Message(m sqlc.ProviderMigration) *plansv1.ProviderMigration {
	out := &plansv1.ProviderMigration{
		Id: m.ID.String(), SubscriptionId: m.SubscriptionID.String(), NewSubscriptionId: convert.UUIDStr(m.NewSubscriptionID),
		FromProvider: m.FromProvider, ToProvider: m.ToProvider, State: migrationStates[m.State], CreateTime: timestamppb.New(m.CreatedAt),
	}
	if m.CutoverAt != nil {
		out.CutoverTime = timestamppb.New(*m.CutoverAt)
	}
	return out
}

// Queries reads migrations.
type Queries struct{ db *store.DB }

// New returns the migration queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Get returns a migration.
func (r *Queries) Get(ctx context.Context, migrationID string) (*plansv1.ProviderMigration, error) {
	id, err := uuid.Parse(migrationID)
	if err != nil {
		return nil, domain.Invalid("migration_id", "must be a UUID")
	}
	var out *plansv1.ProviderMigration
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		mig, err := q.GetProviderMigration(ctx, id)
		if err != nil {
			return err
		}
		out = Message(mig)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return out, nil
}
