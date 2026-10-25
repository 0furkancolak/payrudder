// Package query reads the payments an operator recorded against invoices. It never writes.
package query

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// ForInvoice lists the payments recorded against an invoice, oldest first, in tx (tenant-scoped, or the system's).
func ForInvoice(ctx context.Context, tx pgx.Tx, invoice uuid.UUID) ([]sqlc.ListInvoicePaymentsRow, error) {
	return sqlc.New(tx).ListInvoicePayments(ctx, invoice)
}
