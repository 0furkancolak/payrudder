package command

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	creditscommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/credits/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// linksPaid records that the invoice was paid through its payment links (its own, and the parents of the child links an ad-hoc link handed out) and tells the
// tenant's endpoints once per link. Paying the same invoice again is not possible: PAID is reached once.
func (e *Engine) linksPaid(ctx context.Context, tx pgx.Tx, s *saga, now time.Time) error {
	links, err := sqlc.New(tx).MarkPaymentLinksPaid(ctx, sqlc.MarkPaymentLinksPaidParams{TenantID: s.TenantID, InvoiceID: &s.InvoiceID})
	if err != nil {
		return err
	}
	for _, l := range links {
		if err := tenantevents.Write(ctx, tx, s.TenantID, tenantevents.EventPaymentLinkPaid, l.ID.String()+"|"+s.InvoiceID.String(), now, tenantevents.PaymentLinkPaidData(l, s.InvoiceID, s.Number, s.Currency, s.TotalMinor)); err != nil {
			return err
		}
		if err := e.creditsPurchased(ctx, tx, s, l, now); err != nil {
			return err
		}
	}
	return nil
}

// creditsPurchased is the post-paid hook of a credit-pack link: the credits are granted in the same transaction that marks the invoice paid, keyed by the invoice,
// so however often the confirmation arrives (a duplicate webhook, a replayed settlement, a retried saga step) they are granted once. Other link kinds do nothing.
func (e *Engine) creditsPurchased(ctx context.Context, tx pgx.Tx, s *saga, l sqlc.MarkPaymentLinksPaidRow, now time.Time) error {
	if l.Kind != "credit_pack" || l.CustomerID == nil || l.Credits == nil {
		return nil
	}
	p := creditscommand.Purchase{
		TenantID: s.TenantID, CustomerID: *l.CustomerID, CustomerExternalID: store.Deref(l.CustomerExternalID), LinkID: l.ID, PackID: l.PackID, PackName: store.Deref(l.Description),
		Credits: *l.Credits, InvoiceID: s.InvoiceID, InvoiceNo: s.Number, Currency: s.Currency, AmountMinor: s.TotalMinor, Now: now,
	}
	// A coupon with bonus credits adds them to the same lot, so they expire with the rest.
	bonus, err := sqlc.New(tx).GetInvoiceBonusCredits(ctx, sqlc.GetInvoiceBonusCreditsParams{TenantID: s.TenantID, ID: s.InvoiceID})
	if err != nil {
		return err
	}
	p.Credits += bonus
	p.BonusCredits = bonus
	if l.Quantity != nil {
		p.Quantity = *l.Quantity
	}
	if l.ValidityDays != nil {
		p.ValidityDays = *l.ValidityDays
	}
	return creditscommand.PurchasePaid(ctx, tx, p)
}
