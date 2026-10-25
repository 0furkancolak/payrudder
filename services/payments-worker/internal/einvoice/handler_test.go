package einvoice

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store"
)

type noIssuances struct{}

func (noIssuances) Begin(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (bool, error) {
	return true, nil
}
func (noIssuances) Get(context.Context, uuid.UUID, uuid.UUID) (store.Issuance, bool, error) {
	return store.Issuance{}, false, nil
}
func (noIssuances) Complete(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) error {
	return nil
}
func (noIssuances) Cancel(context.Context, uuid.UUID, uuid.UUID) error { return nil }

// A credit note whose original document id is not a UUID can never be executed: it is a poison message, not a panic that crashes the worker on every delivery.
func TestCreditNoteWithABadOriginalIDIsPoison(t *testing.T) {
	h := &Handler{Issuances: noIssuances{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c := command()
	c.Kind = "credit_note"
	c.OriginalDocumentId = "not-a-uuid"
	cmd := &commandsv1.CommandEnvelope{TenantId: uuid.NewString(), Environment: plansv1.Environment_ENVIRONMENT_SANDBOX,
		Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: c}}

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Handle panicked: %v", r)
			}
		}()
		_, err = h.Handle(context.Background(), cmd)
	}()
	if !errors.Is(err, workerx.ErrPoison) {
		t.Fatalf("err = %v, want ErrPoison", err)
	}
}
