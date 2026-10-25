// Package query reads the payment ledger. It never writes.
package query

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
	// MaxLookup is the most invoice ids one lookup takes.
	MaxLookup = 200
)

// Deps is what the queries read.
type Deps struct {
	Ledger store.Ledger
}

// Limit reads the ?limit= of a listing: 1 to 1000, 100 when absent.
func Limit(raw string) (int, error) {
	if raw == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		return 0, refuse.New(http.StatusBadRequest, "payments.ledger.bad_limit", "limit must be 1..1000")
	}
	return n, nil
}

// List is the newest rows of the tenant's ledger.
func List(ctx context.Context, d Deps, tenant uuid.UUID, limit int) ([]store.Row, error) {
	rows, err := d.Ledger.List(ctx, tenant, limit)
	if err != nil {
		return nil, fmt.Errorf("list the ledger: %w", err)
	}
	return rows, nil
}

// Lookup is the tenant's ledger rows of those invoices. An id of another tenant's invoice simply has no rows here.
func Lookup(ctx context.Context, d Deps, tenant uuid.UUID, invoiceIDs []string) ([]store.LedgerLine, error) {
	if len(invoiceIDs) > MaxLookup {
		return nil, refuse.New(http.StatusBadRequest, "payments.ledger.too_many_invoices", fmt.Sprintf("at most %d invoice ids", MaxLookup))
	}
	ids := make([]uuid.UUID, 0, len(invoiceIDs))
	for _, v := range invoiceIDs {
		id, err := uuid.Parse(v)
		if err != nil {
			return nil, refuse.New(http.StatusBadRequest, "payments.invoice.bad_ids", "invoice_ids must be UUIDs")
		}
		ids = append(ids, id)
	}
	rows, err := d.Ledger.LookupInvoices(ctx, tenant, ids)
	if err != nil {
		return nil, fmt.Errorf("look up the ledger of invoices: %w", err)
	}
	return rows, nil
}
