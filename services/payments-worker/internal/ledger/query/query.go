// Package query reads the payment ledger. It never writes.
package query

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

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
	// Attempts and Window answer how long a payment that waits for the customer has: Window is PAYMENTS_TRANSFER_WINDOW_DAYS, the one the status query judges by.
	Attempts store.Attempts
	Window   time.Duration
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

// ChargeWindow is the window a payment that waits for the customer (3-D Secure) has: when the invoice's newest charge was first tried and when the status query starts to
// judge it declined (authentication_required), the same moment handler.providerStatus uses. Found is false for an invoice with no charge attempt.
type ChargeWindow struct {
	Found      bool       `json:"found"`
	Started    *time.Time `json:"started_time,omitempty"`
	WindowEnds *time.Time `json:"window_ends_time,omitempty"`
}

// Window reads the window of an invoice's newest charge.
func Window(ctx context.Context, d Deps, tenant uuid.UUID, invoice string) (ChargeWindow, error) {
	inv, err := uuid.Parse(invoice)
	if err != nil {
		return ChargeWindow{}, refuse.New(http.StatusBadRequest, "payments.invoice.bad_ids", "the invoice must be a UUID")
	}
	started, found, err := d.Attempts.LatestChargeStart(ctx, tenant, inv)
	if err != nil {
		return ChargeWindow{}, fmt.Errorf("read the start of the newest charge: %w", err)
	}
	if !found {
		return ChargeWindow{}, nil
	}
	started = started.UTC()
	out := ChargeWindow{Found: true, Started: &started}
	if d.Window > 0 {
		end := started.Add(d.Window)
		out.WindowEnds = &end
	}
	return out, nil
}
