package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// expiryBatch is how many expired links one pass handles.
const expiryBatch = 200

// ExpireLinks tells the tenants about the payment links that expired unpaid (payment_link.expired, once per link) and voids the invoices an expired ad-hoc link made
// that nobody paid: nobody can pay them any more. A single-use link that was paid is not expired in this sense; it was used. Every step is idempotent, so a crash is
// repaired by the next pass.
func (b *Backend) ExpireLinks(ctx context.Context) error {
	due, err := b.q.ListLinksToExpire(ctx, expiryBatch)
	if err != nil {
		return err
	}
	for _, d := range due {
		var voids []uuid.UUID
		err := pgxx.InTx(ctx, b.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			q := b.q.WithTx(tx)
			l, err := q.LockLinkToExpire(ctx, sqlc.LockLinkToExpireParams(d))
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // another pod has it, or it was revoked meanwhile
			}
			if err != nil {
				return err
			}
			if factory(l.Kind) {
				if voids, err = q.ChildInvoicesToVoid(ctx, sqlc.ChildInvoicesToVoidParams(d)); err != nil {
					return err
				}
			}
			if err := tenantevents.Write(ctx, tx, d.TenantID, tenantevents.EventPaymentLinkExpired, l.ID.String(), b.cfg.Now(), tenantevents.PaymentLinkExpiredData(l)); err != nil {
				return err
			}
			return q.MarkLinkExpiredNotified(ctx, sqlc.MarkLinkExpiredNotifiedParams(d))
		})
		if err != nil {
			return err
		}
		VoidUnpaid(ctx, b.cfg.Pool, b.cfg.Engine, d.TenantID, voids) // a payment in flight keeps the invoice
	}
	return nil
}
