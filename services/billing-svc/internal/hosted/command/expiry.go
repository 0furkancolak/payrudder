package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// expiryBatch is how many expired links one pass handles.
const expiryBatch = 200

// ExpireLinks tells the tenants about the payment links that expired unpaid (payment_link.expired, once per link) and voids the invoices an expired ad-hoc link made
// that nobody paid: nobody can pay them any more. A single-use link that was paid is not expired in this sense; it was used. Every step is idempotent, so a crash is
// repaired by the next pass.
//
// It looks in every place a tenant's rows can be (the pooled database, each tenant placed in a cell, ADR 0018), and acts on a link only in the place it was found.
func (b *Backend) ExpireLinks(ctx context.Context) error {
	return b.cfg.DB.Each(ctx, b.expireLinks)
}

func (b *Backend) expireLinks(ctx context.Context, s tenantdb.Scope) error {
	due, err := s.Scan().ListLinksToExpire(ctx, expiryBatch)
	if err != nil {
		return err
	}
	for _, d := range due {
		var voids []uuid.UUID
		err := s.InTx(ctx, d.TenantID, pgx.TxOptions{}, func(tx pgx.Tx) error {
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
		if tenantdb.Skippable(err) {
			continue // the tenant is moving: the next pass finds the link again
		}
		if err != nil {
			return err
		}
		voidUnpaid(ctx, b.cfg.DB, b.cfg.Engine, d.TenantID, voids) // a payment in flight keeps the invoice
	}
	return nil
}
