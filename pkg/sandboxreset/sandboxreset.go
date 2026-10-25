// Package sandboxreset is the service side of resetting a sandbox (phase 45 E). A service that keeps data of a tenant implements one function, Purger, which
// deletes that tenant's customer-side data (usage, invoices, payments) and keeps its configuration; this package wraps it in SandboxResetService, so that the
// refusals are the same everywhere: operator only, a sandbox tenant only, and a clear answer to anything else.
//
// A reset is never a "delete everything with this tenant_id": each service lists, in SQL it can be reviewed in, what goes. Coverage is checked by a test
// (MissingTables): a table with a tenant_id column that is neither deleted nor kept on purpose fails it, so a new table cannot be forgotten.
package sandboxreset

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	adminv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/admin/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Purger deletes one sandbox tenant's data in a service and returns the rows deleted, by table. It must be idempotent.
type Purger func(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error)

// Server implements adminv1.SandboxResetServiceServer over a Purger.
type Server struct {
	adminv1.UnimplementedSandboxResetServiceServer
	purge Purger
}

// New returns the service.
func New(p Purger) *Server { return &Server{purge: p} }

// ResetTenant implements adminv1.SandboxResetServiceServer.
func (s *Server) ResetTenant(ctx context.Context, req *adminv1.ResetTenantRequest) (*adminv1.ResetTenantResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	if e, err := env.FromProto(req.GetEnvironment()); err != nil || !e.IsSandbox() {
		return nil, errkey.New(codes.FailedPrecondition, "admin.sandbox.live_refused", "only a sandbox tenant can be reset")
	}
	id, err := uuid.Parse(req.GetTenantId())
	if err != nil || id == uuid.Nil {
		return nil, errkey.New(codes.InvalidArgument, "admin.sandbox.bad_tenant", "tenant_id must be a UUID")
	}
	deleted, err := s.purge(ctx, id)
	if err != nil {
		return nil, err
	}
	return &adminv1.ResetTenantResponse{Deleted: deleted}, nil
}

// MissingTables lists the base tables of the database that have a tenant_id column and are neither in deleted nor in kept. Partitions of a table are not listed:
// the parent is. A service's test calls it with its purge's tables and the tables it keeps on purpose, and fails when the result is not empty.
func MissingTables(ctx context.Context, pool *pgxpool.Pool, deleted, kept []string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'public'
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p') AND NOT c.relispartition
		ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("sandboxreset: list tenant tables: %w", err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !slices.Contains(deleted, name) && !slices.Contains(kept, name) {
			missing = append(missing, name)
		}
	}
	return missing, rows.Err()
}
