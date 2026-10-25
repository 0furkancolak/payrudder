package tenantevents

import (
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Event types billing-svc announces to the tenant's webhook endpoints (plans-svc has the full list).
//
//nolint:gosec // event type names (credit_note.*), not credentials
const (
	EventInvoiceFinalized       = "invoice.finalized"
	EventInvoicePaid            = "invoice.paid"
	EventInvoiceVoided          = "invoice.voided"
	EventInvoiceUncollectible   = "invoice.uncollectible"
	EventCreditNoteCreated      = "credit_note.created"
	EventCreditNoteRefunded     = "credit_note.refunded"
	EventCreditNoteRefundFailed = "credit_note.refund_failed"
	EventPaymentLinkCreated     = "payment_link.created"
	EventPaymentLinkPaid        = "payment_link.paid"
	EventPaymentLinkExpired     = "payment_link.expired"
)

// InvoiceData is the data of an invoice event; amounts are strings like in the REST API.
func InvoiceData(invoiceID, customerID uuid.UUID, customerExternalID, number, period, currency string, total int64, status string) map[string]any {
	return map[string]any{
		"invoice_id": invoiceID.String(), "number": number, "customer_id": customerID.String(), "customer_external_id": customerExternalID,
		"period_id": period, "currency": currency, "total_minor": strconv.FormatInt(total, 10), "status": status,
	}
}

// PaymentLinkPaidData is the data of payment_link.paid; amounts are strings like in the REST API.
func PaymentLinkPaidData(l sqlc.MarkPaymentLinksPaidRow, invoiceID uuid.UUID, number, currency string, total int64) map[string]any {
	return map[string]any{
		"link_id": l.ID.String(), "kind": l.Kind, "invoice_id": invoiceID.String(), "invoice_number": number, "customer_external_id": store.Deref(l.CustomerExternalID),
		"currency": currency, "total_minor": strconv.FormatInt(total, 10), "paid_count": l.PaidCount,
	}
}

// PaymentLinkExpiredData is the data of payment_link.expired.
func PaymentLinkExpiredData(l sqlc.LockLinkToExpireRow) map[string]any {
	d := map[string]any{"link_id": l.ID.String(), "kind": l.Kind, "expires_at": l.ExpiresAt.UTC().Format(time.RFC3339)}
	if l.InvoiceID != nil {
		d["invoice_id"] = l.InvoiceID.String()
	}
	if l.AmountMinor != nil {
		d["amount_minor"] = strconv.FormatInt(*l.AmountMinor, 10)
	}
	if l.Currency != nil {
		d["currency"] = *l.Currency
	}
	if l.CustomerExternalID != nil {
		d["customer_external_id"] = *l.CustomerExternalID
	}
	return d
}
