package query

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Default and largest page of ListTenants.
const (
	defaultPageSize = 500
	maxPageSize     = 1000
)

// Queries reads any tenant, for an operator, on the system pool (role plans_system, BYPASSRLS). The functions above read the caller's own tenant.
type Queries struct{ pool *pgxpool.Pool }

// New returns the operator tenant queries on the system pool.
func New(systemPool *pgxpool.Pool) *Queries { return &Queries{pool: systemPool} }

// Get returns one tenant.
func (q *Queries) Get(ctx context.Context, id uuid.UUID) (*plansv1.Tenant, error) {
	db := sqlc.New(q.pool)
	t, err := db.GetTenant(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	one := []sqlc.Tenant{t}
	if err := convert.AccountBan(ctx, db, t.AccountID, one); err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return convert.Tenant(one[0]), nil
}

// List returns a page of tenants in id order. The token is the id of the last tenant of the previous page.
func (q *Queries) List(ctx context.Context, pageSize int32, pageToken string) ([]*plansv1.Tenant, string, error) {
	size, ok := page.Size(pageSize, defaultPageSize, maxPageSize)
	if !ok {
		return nil, "", domain.Invalid("page_size", "must be 1..1000")
	}
	after := uuid.Nil
	if pageToken != "" {
		var err error
		if after, err = uuid.Parse(pageToken); err != nil {
			return nil, "", domain.Invalid("page_token", "invalid")
		}
	}
	db := sqlc.New(q.pool)
	rows, err := db.ListTenants(ctx, sqlc.ListTenantsParams{After: after, PageSize: size + 1})
	if err != nil {
		return nil, "", convert.Err(err, "tenants")
	}
	banned, err := db.ListBannedAccountIDs(ctx)
	if err != nil {
		return nil, "", convert.Err(err, "tenants")
	}
	for i := range rows {
		if slices.Contains(banned, rows[i].AccountID) {
			convert.BanLive(&rows[i])
		}
	}
	var next string
	var out []*plansv1.Tenant
	for i, t := range rows {
		if int32(i) == size { //nolint:gosec // bounded by page size
			next = rows[i-1].ID.String()
			break
		}
		out = append(out, convert.Tenant(t))
	}
	return out, next, nil
}
