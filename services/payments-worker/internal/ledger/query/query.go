// Package query reads the payment ledger. It never writes.
package query

import (
	"context"
	"errors"
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
	// Disputes lists the cases a customer opened with their bank.
	Disputes store.Disputes
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

// Disputes lists the tenant's cases, newest first: of one invoice when invoice is not empty, of one outcome (open, won or lost) when that is not empty.
func Disputes(ctx context.Context, d Deps, tenant uuid.UUID, invoice, outcome string, limit int) ([]store.Dispute, error) {
	var inv uuid.UUID
	if invoice != "" {
		var err error
		if inv, err = uuid.Parse(invoice); err != nil {
			return nil, refuse.New(http.StatusBadRequest, "payments.invoice.bad_ids", "invoice_id must be a UUID")
		}
	}
	switch outcome {
	case "", "open", "won", "lost":
	default:
		return nil, refuse.New(http.StatusBadRequest, "common.invalid_argument", "outcome must be open, won or lost")
	}
	rows, err := d.Disputes.List(ctx, tenant, inv, outcome, limit)
	if err != nil {
		return nil, fmt.Errorf("list the disputes: %w", err)
	}
	return rows, nil
}

// DisputeID reads the id of a case from a path: the number Payrudder gave it.
func DisputeID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		return 0, refuse.New(http.StatusBadRequest, "payments.dispute.bad_id", "the dispute id is the number Payrudder gave the case")
	}
	return id, nil
}

// DisputeHistory is what the seller did with a case, newest first: notes, and the marks that it was handled.
func DisputeHistory(ctx context.Context, d Deps, tenant uuid.UUID, id int64) ([]store.DisputeEvent, error) {
	ev, err := d.Disputes.Events(ctx, tenant, id)
	if errors.Is(err, store.ErrNoDispute) {
		return nil, refuse.New(http.StatusNotFound, "payments.dispute.not_found", "no such dispute")
	}
	if err != nil {
		return nil, fmt.Errorf("read the history of dispute %d: %w", id, err)
	}
	return ev, nil
}
