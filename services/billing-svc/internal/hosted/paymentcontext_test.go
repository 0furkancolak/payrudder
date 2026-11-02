package hosted

import (
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted/query"
	invoicequery "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/query"
	sagamodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/model"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// A provider that vets the payer (iyzico) reads the customer record, so the payment context names the customer the invoice is out to; a guest has none.
func TestThePaymentContextNamesTheCustomerOfTheInvoice(t *testing.T) {
	tenant := uuid.New()
	page := &query.Page{Invoice: &invoicequery.Invoice{Row: sqlc.GetInvoiceRow{ID: uuid.New(), Number: "INV-1", TotalMinor: 5000, Currency: "TRY"}},
		Snapshot: sagamodel.Snapshot{CustomerExternalID: "ext-1", CustomerEmail: "ada@example.com"}, SagaID: uuid.New(), Environment: "live"}
	got := paymentContext(tenant, page)
	if got.GetCustomerExternalId() != "ext-1" || got.GetCustomerEmail() != "ada@example.com" || got.GetTenantId() != tenant.String() {
		t.Errorf("context = %v", got)
	}
	page.Snapshot.CustomerExternalID = ""
	if got := paymentContext(tenant, page); got.GetCustomerExternalId() != "" {
		t.Errorf("a guest has no customer: %v", got)
	}
}
