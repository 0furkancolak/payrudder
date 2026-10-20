package runs

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
)

// invoice finalizes every draft of the run (number, open, saga started) and reports whether the run is complete:
// all its invoices' sagas reached COMPLETED or VOIDED.
func (m *Manager) invoice(ctx context.Context, run *Run) (bool, error) {
	for {
		rows, err := m.cfg.Pool.Query(ctx, `SELECT id FROM invoices WHERE tenant_id = $1 AND period_id = $2 AND kind = 'REGULAR' AND status = 'draft'
			ORDER BY id LIMIT $3`, run.TenantID, run.PeriodID, m.cfg.PageSize)
		if err != nil {
			return false, err
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return false, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := m.Finalize(ctx, run.TenantID, id); err != nil {
				return false, err
			}
		}
	}
	var open int
	if err := m.cfg.Pool.QueryRow(ctx, `
		SELECT count(*) FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
		WHERE i.tenant_id = $1 AND i.period_id = $2 AND i.kind = 'REGULAR' AND (s.state IS NULL OR s.state NOT IN ('COMPLETED','VOIDED'))`,
		run.TenantID, run.PeriodID).Scan(&open); err != nil {
		return false, err
	}
	return open == 0, nil
}

// Finalize assigns the next gap-free number of the tenant and year, opens the invoice and starts its saga, in one
// transaction: a rollback leaves neither a number consumed nor a saga behind. Finalizing a finalized invoice is a no-op.
func (m *Manager) Finalize(ctx context.Context, tenantID, invoiceID uuid.UUID) error {
	return pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var status, env, currency, period, external string
		var total int64
		var customer uuid.UUID
		err := tx.QueryRow(ctx, `SELECT status, snapshot->>'environment', currency, period_id, total_minor, customer_id, COALESCE(snapshot->>'customer_external_id', '') FROM invoices
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, invoiceID).Scan(&status, &env, &currency, &period, &total, &customer, &external)
		if err != nil {
			return err
		}
		if status != "draft" {
			return nil
		}
		now := m.cfg.Now()
		year := now.UTC().Year()
		if _, err := tx.Exec(ctx, `INSERT INTO invoice_number_seq (tenant_id, year, next_value) VALUES ($1,$2,1) ON CONFLICT DO NOTHING`, tenantID, year); err != nil {
			return err
		}
		var seq int64
		if err := tx.QueryRow(ctx, `SELECT next_value FROM invoice_number_seq WHERE tenant_id = $1 AND year = $2 FOR UPDATE`, tenantID, year).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE invoice_number_seq SET next_value = next_value + 1 WHERE tenant_id = $1 AND year = $2`, tenantID, year); err != nil {
			return err
		}
		number := Number(env, year, seq)
		if _, err := tx.Exec(ctx, `UPDATE invoices SET number = $3, status = 'open', finalized_at = $4 WHERE tenant_id = $1 AND id = $2`,
			tenantID, invoiceID, number, now); err != nil {
			return err
		}
		if err := saga.FinalizedFact(ctx, tx, now, tenantID, invoiceID, customer, period, number, currency, total); err != nil {
			return err
		}
		if err := saga.TenantEvent(ctx, tx, tenantID, saga.EventInvoiceFinalized, invoiceID.String(), now,
			saga.InvoiceData(invoiceID, customer, external, number, period, currency, total, "open")); err != nil {
			return err
		}
		_, err = m.cfg.Engine.Start(ctx, tx, tenantID, invoiceID)
		return err
	})
}

// Number formats an invoice number: INV-2026-000123 for live tenants, SBX-2026-000123 for sandbox ones, so a sandbox
// invoice can't be mistaken for a real one.
func Number(environment string, year int, seq int64) string {
	prefix := "INV"
	if environment == "sandbox" {
		prefix = "SBX"
	}
	return fmt.Sprintf("%s-%d-%06d", prefix, year, seq)
}
