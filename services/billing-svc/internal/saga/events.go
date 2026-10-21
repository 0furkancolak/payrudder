package saga

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/quotify/services/billing-svc/internal/tenantevents"
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

// TenantEvent writes an event for the tenant's endpoints in tx. key says which occurrence it is (an invoice id, a credit note id and an outcome): the same
// (tenant, type, key) is one event however often it is written.
func TenantEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, typ, key string, at time.Time, data map[string]any) error {
	return tenantevents.Write(ctx, tx, tenantID, typ, key, at, data)
}

// InvoiceData is the data of an invoice event; amounts are strings like in the REST API.
func InvoiceData(invoiceID, customerID uuid.UUID, customerExternalID, number, period, currency string, total int64, status string) map[string]any {
	return map[string]any{
		"invoice_id": invoiceID.String(), "number": number, "customer_id": customerID.String(), "customer_external_id": customerExternalID,
		"period_id": period, "currency": currency, "total_minor": strconv.FormatInt(total, 10), "status": status,
	}
}
