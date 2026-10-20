package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var tenantEventNS = uuid.MustParse("3b9d6e12-7a40-5c85-91f3-e0b2a8d45c17")

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
)

// TenantEvent writes an event for the tenant's endpoints in tx. key says which occurrence it is (an invoice id, a credit note id and an outcome): the same
// (tenant, type, key) is one event however often it is written.
func TenantEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, typ, key string, at time.Time, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	id := uuid.NewSHA1(tenantEventNS, fmt.Appendf(nil, "%s|%s|%s", tenantID, typ, key))
	_, err = tx.Exec(ctx, `INSERT INTO tenant_event_outbox (tenant_id, event_id, type, data, occurred_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		tenantID, id, typ, b, at)
	return err
}

// InvoiceData is the data of an invoice event; amounts are strings like in the REST API.
func InvoiceData(invoiceID, customerID uuid.UUID, customerExternalID, number, period, currency string, total int64, status string) map[string]any {
	return map[string]any{
		"invoice_id": invoiceID.String(), "number": number, "customer_id": customerID.String(), "customer_external_id": customerExternalID,
		"period_id": period, "currency": currency, "total_minor": strconv.FormatInt(total, 10), "status": status,
	}
}
