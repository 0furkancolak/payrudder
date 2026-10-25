package command

import (
	"testing"

	"github.com/google/uuid"

	einvoicemodel "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/model"
)

func TestEInvoiceIdsAreDeterministicAndDistinct(t *testing.T) {
	tenant, source := uuid.New(), uuid.New()
	a := DocumentID(tenant, einvoicemodel.KindInvoice, source)
	if a != DocumentID(tenant, einvoicemodel.KindInvoice, source) {
		t.Error("the id of one source changes")
	}
	if a == DocumentID(tenant, einvoicemodel.KindCreditNote, source) || a == DocumentID(uuid.New(), einvoicemodel.KindInvoice, source) {
		t.Error("two documents share an id")
	}
	if CommandID(a, "issue", 1) == CommandID(a, "issue", 2) || CommandID(a, "issue", 1) == CommandID(a, "cancel", 1) {
		t.Error("two commands share an id")
	}
}
