package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/rabbitx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/workercmd"
)

var einvoiceNS = uuid.MustParse("3b8e6d41-72c9-5a04-9f1d-c4e0a7b25d68")

// DocumentID is the deterministic id of the document of a source (an invoice or a credit note). It is the document's UUID at the integrator.
func DocumentID(tenantID uuid.UUID, kind string, sourceID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(einvoiceNS, fmt.Appendf(nil, "%s|%s|%s", tenantID, kind, sourceID))
}

// CommandID is the command id of one attempt of one action on a document.
func CommandID(docID uuid.UUID, action string, attempt int) uuid.UUID {
	return uuid.NewSHA1(einvoiceNS, fmt.Appendf(nil, "cmd|%s|%s|%d", docID, action, attempt))
}

// Publish writes the outbox row of an IssueEInvoice command. The document id travels as saga id, so the reply finds its document.
func Publish(ctx context.Context, tx pgx.Tx, now time.Time, tenantID uuid.UUID, environment string, cmd *commandsv1.IssueEInvoice, attempt int) error {
	docID, err := uuid.Parse(cmd.GetDocumentId())
	if err != nil {
		return err
	}
	id := CommandID(docID, cmd.GetAction(), attempt)
	// The environment is stamped from the tenant's snapshot and is never defaulted: a document whose snapshot names none is not sent anywhere.
	return workercmd.Send(ctx, tx, workercmd.Header{
		CommandID: id, SagaID: docID, TenantID: tenantID, Attempt: attempt, IssuedAt: now, Environment: environment, Subject: "e-invoice command for document " + docID.String(),
	}, &commandsv1.CommandEnvelope{Command: &commandsv1.CommandEnvelope_IssueEInvoice{IssueEInvoice: cmd}}, rabbitx.QueueEInvoiceIssue)
}

// Enqueue creates the document of an invoice or credit note and sends its first command, in the caller's transaction. A second call for the same
// source creates nothing (the unique index), so a replayed transition issues no second document. It reports whether a document was created.
func Enqueue(ctx context.Context, tx pgx.Tx, now time.Time, tenantID uuid.UUID, environment, kind string, invoiceID uuid.UUID, creditNoteID *uuid.UUID, number string, cmd *commandsv1.IssueEInvoice) (bool, error) {
	src := invoiceID
	if creditNoteID != nil {
		src = *creditNoteID
	}
	docID := DocumentID(tenantID, kind, src)
	cmd.Action, cmd.DocumentId, cmd.Kind = "issue", docID.String(), kind
	raw, err := proto.Marshal(cmd)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New(tx).InsertEInvoiceDocument(ctx, sqlc.InsertEInvoiceDocumentParams{TenantID: tenantID, ID: docID, Kind: kind, InvoiceID: invoiceID, CreditNoteID: creditNoteID,
		Number: number, Environment: environment, Request: raw, Now: now})
	if err != nil || n == 0 {
		return false, err
	}
	return true, Publish(ctx, tx, now, tenantID, environment, cmd, 1)
}
