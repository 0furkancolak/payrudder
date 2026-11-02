// Package auditlog reads billing-svc's audit log for AuditService (pkg/audit). A tenant's entries are written in its own transactions, so they are where its other rows
// are: the pooled database, or its schema in a cell.
package auditlog

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Source is auditgrpc.Source over billing-svc's databases.
type Source struct{ DB *tenantdb.DB }

// Tenant reads a tenant's entries wherever its rows are.
func (s Source) Tenant(ctx context.Context, f audit.Filter) (rows []audit.Row, err error) {
	err = s.DB.InTx(ctx, f.TenantID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		rows, err = audit.List(ctx, tx, f)
		return err
	})
	return rows, err
}

// All reads every entry of the pooled database: the operator's view. The entries of a tenant placed in a cell are read by asking for that tenant.
func (s Source) All(ctx context.Context, f audit.Filter) ([]audit.Row, error) {
	return audit.List(ctx, s.DB.Pool(), f)
}
