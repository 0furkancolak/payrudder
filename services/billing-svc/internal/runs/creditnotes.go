package runs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/quotify/pkg/pgxx"
)

// Refusals of CreateCreditNote.
var (
	ErrInvoiceNotFound = errors.New("credit note: no such invoice")
	// ErrNotPaid: a credit note corrects an invoice the customer has paid. An open invoice is voided instead; a declined one is dunned.
	ErrNotPaid     = errors.New("credit note: only a paid invoice can be credited")
	ErrBadAmount   = errors.New("credit note: the amount must be positive and at most what is left of the invoice")
	ErrNothingLeft = errors.New("credit note: the invoice has been credited in full")
)

// CreditNote is a numbered correction of a paid invoice.
type CreditNote struct {
	ID, CustomerID, InvoiceID uuid.UUID
	Number, InvoiceNumber     string
	Currency, Reason          string
	TotalMinor                int64
	CreatedAt                 time.Time
}

// CreditNoteNumber formats a credit note number: CN-2026-000007 for live tenants, SBX-CN-2026-000007 for sandbox ones.
func CreditNoteNumber(environment string, year int, seq int64) string {
	prefix := "CN"
	if environment == "sandbox" {
		prefix = "SBX-CN"
	}
	return fmt.Sprintf("%s-%d-%06d", prefix, year, seq)
}

// CreateCreditNote issues a credit note against a paid invoice and adds its amount to the customer's credit balance, in one transaction: a rollback
// leaves neither a number consumed nor a credit behind. amount 0 means whatever is left of the invoice. The notes of an invoice never exceed it.
func (m *Manager) CreateCreditNote(ctx context.Context, tenantID, invoiceID uuid.UUID, amount int64, reason string) (*CreditNote, error) {
	var out *CreditNote
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var (
			status, currency, env string
			number                *string
			total                 int64
			customer              uuid.UUID
		)
		err := tx.QueryRow(ctx, `SELECT status, currency, COALESCE(snapshot->>'environment', 'live'), number, total_minor, customer_id FROM invoices
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, invoiceID).Scan(&status, &currency, &env, &number, &total, &customer)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvoiceNotFound
		}
		if err != nil {
			return err
		}
		if status != "paid" {
			return ErrNotPaid
		}
		var credited int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_minor), 0) FROM credit_notes WHERE tenant_id = $1 AND invoice_id = $2`, tenantID, invoiceID).Scan(&credited); err != nil {
			return err
		}
		left := total - credited
		switch {
		case left <= 0:
			return ErrNothingLeft
		case amount == 0:
			amount = left
		case amount < 0 || amount > left:
			return ErrBadAmount
		}
		now := m.cfg.Now()
		year := now.UTC().Year()
		if _, err := tx.Exec(ctx, `INSERT INTO credit_note_seq (tenant_id, year, next_value) VALUES ($1,$2,1) ON CONFLICT DO NOTHING`, tenantID, year); err != nil {
			return err
		}
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT next_value FROM credit_note_seq WHERE tenant_id = $1 AND year = $2 FOR UPDATE`, tenantID, year).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE credit_note_seq SET next_value = next_value + 1 WHERE tenant_id = $1 AND year = $2`, tenantID, year); err != nil {
			return err
		}
		id := uuid.New()
		cn := &CreditNote{ID: id, CustomerID: customer, InvoiceID: invoiceID, Number: CreditNoteNumber(env, year, seq), Currency: currency, Reason: reason, TotalMinor: amount, CreatedAt: now}
		if number != nil {
			cn.InvoiceNumber = *number
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credit_notes (tenant_id, id, customer_id, invoice_id, number, currency, total_minor, reason, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			tenantID, id, customer, invoiceID, cn.Number, currency, amount, reason, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO credit_balances (tenant_id, customer_id, id, amount_minor, source_credit_note_id, currency) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenantID, customer, uuid.New(), amount, id, currency); err != nil {
			return err
		}
		out = cn
		return nil
	})
	return out, err
}
