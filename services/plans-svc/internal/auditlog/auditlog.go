// Package auditlog reads plans-svc's audit log for AuditService (pkg/audit). Entries written in a tenant's own transaction are in the pooled database or, for a
// tenant placed in a cell, in the tenant's schema there; entries written on the system pool (API keys, the tenant's status, an account's members, sign-ins) are
// always in the pooled database.
package auditlog

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Source is auditgrpc.Source over plans-svc's databases.
type Source struct{ Places *placement.Places }

// Tenant reads a tenant's entries and those of its account that belong to no tenant (its members).
func (s Source) Tenant(ctx context.Context, f audit.Filter) ([]audit.Row, error) {
	t, err := sqlc.New(s.Places.Pool()).GetTenant(ctx, f.TenantID)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	scoped := f
	scoped.AccountID = t.AccountID
	rows, err := audit.List(ctx, s.Places.Pool(), scoped)
	if err != nil {
		return nil, err
	}
	inCell, err := s.Places.InCell(ctx, f.TenantID)
	if err != nil || !inCell {
		return rows, err
	}
	var own []audit.Row
	if err := s.Places.Tx(ctx, f.TenantID, func(tx pgx.Tx) (err error) {
		own, err = audit.List(ctx, tx, f)
		return err
	}); err != nil {
		return nil, err
	}
	return audit.Merge(f.Limit, rows, own), nil
}

// All reads every entry of the pooled database: the operator's view. The entries of a tenant placed in a cell are read by asking for that tenant.
func (s Source) All(ctx context.Context, f audit.Filter) ([]audit.Row, error) {
	return audit.List(ctx, s.Places.Pool(), f)
}
