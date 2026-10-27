// Package query lists the apps of an account for an operator.
package query

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads accounts and apps on the system pool.
type Queries struct{ pool *pgxpool.Pool }

// New returns the account queries on the system pool.
func New(systemPool *pgxpool.Pool) *Queries { return &Queries{pool: systemPool} }

// Apps lists the apps of an account with their tenants, the archived ones included (the operator keeps their billing records in sight).
func (q *Queries) Apps(ctx context.Context, accountID uuid.UUID) ([]*plansv1.App, error) {
	db := sqlc.New(q.pool)
	apps, err := db.ListAccountAppsWithArchived(ctx, accountID)
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	tenants, err := db.ListAccountTenants(ctx, accountID)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return convert.Apps(apps, tenants), nil
}
