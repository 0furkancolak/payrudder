// Package command changes e-documents: applies the worker's answers, sends the commands that are due again, and carries out the operator's retry and cancel.
package command

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/saga"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// ReplyWait is how long a command may stay unanswered before it is sent again.
const ReplyWait = 10 * time.Minute

// Backoff are the delays before automatic attempt 2..6 after a retryable failure; after the sixth attempt the document is failed.
var Backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 12 * time.Hour}

// NextAttempt returns when the next automatic attempt is due after streak attempts, or false when they are used up.
func NextAttempt(now time.Time, streak int) (time.Time, bool) {
	if streak < 1 || streak > len(Backoff) {
		return time.Time{}, false
	}
	return now.Add(Backoff[streak-1]), true
}

// Parties gives the current seller and buyer of an invoice's customer (runs.Manager implements it).
type Parties interface {
	Parties(ctx context.Context, tenantID uuid.UUID, customerExternalID string) (seller, buyer *saga.Party, err error)
}

// Commands changes e-documents over the billing_system pool.
type Commands struct {
	Pool    *pgxpool.Pool
	Parties Parties
	Now     func() time.Time
}

func (c *Commands) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ApplyResult applies the worker's answer to the document it is about, in the reply consumer's transaction. An answer for an unknown document is dropped.
func ApplyResult(ctx context.Context, tx pgx.Tx, now time.Time, tenantID, docID uuid.UUID, res *commandsv1.EInvoiceResult) error {
	q := sqlc.New(tx)
	doc, err := q.LockEInvoiceDocument(ctx, sqlc.LockEInvoiceDocumentParams{TenantID: tenantID, ID: docID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	key, msg := res.GetErrorKey(), truncate(res.GetErrorMessage(), 500)
	switch res.GetState() {
	case "issued":
		// Also from failed: a late answer means the document exists at the integrator, and that fact must not be lost.
		return q.SetEInvoiceIssued(ctx, sqlc.SetEInvoiceIssuedParams{LegalNumber: ptr(res.GetLegalNumber()), ExternalID: res.GetExternalId(), DocumentType: res.GetDocumentType(),
			Provider: res.GetProvider(), XmlKey: ptrOrNil(res.GetXmlObjectKey()), PdfKey: ptrOrNil(res.GetPdfObjectKey()), Now: now, TenantID: tenantID, ID: docID})
	case "cancelled":
		return q.SetEInvoiceCancelled(ctx, sqlc.SetEInvoiceCancelledParams{Now: now, TenantID: tenantID, ID: docID})
	}
	// failed
	if doc.Status == saga.EInvoiceIssued {
		return q.SetEInvoiceCancelFailed(ctx, sqlc.SetEInvoiceCancelFailedParams{ErrorKey: key, Error: msg, Now: now, TenantID: tenantID, ID: docID})
	}
	if res.GetRetryable() {
		if at, ok := NextAttempt(now, int(doc.Streak)); ok {
			return q.SetEInvoiceRetry(ctx, sqlc.SetEInvoiceRetryParams{NextAttemptAt: &at, ErrorKey: key, Error: msg, DocumentType: res.GetDocumentType(), Provider: res.GetProvider(),
				Now: now, TenantID: tenantID, ID: docID})
		}
	}
	return q.SetEInvoiceFailed(ctx, sqlc.SetEInvoiceFailedParams{ErrorKey: key, Error: msg, DocumentType: res.GetDocumentType(), Provider: res.GetProvider(),
		Now: now, TenantID: tenantID, ID: docID})
}

func ptr(s string) *string { return &s }

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// dispatch sends the command of a locked document again as the next attempt.
func dispatch(ctx context.Context, tx pgx.Tx, now time.Time, tenantID, docID uuid.UUID, doc sqlc.LockEInvoiceDocumentRow, cancelReason string) error {
	var cmd commandsv1.IssueEInvoice
	if err := proto.Unmarshal(doc.Request, &cmd); err != nil {
		return err
	}
	q := sqlc.New(tx)
	attempt, err := q.MarkEInvoiceDispatched(ctx, sqlc.MarkEInvoiceDispatchedParams{Now: now, TenantID: tenantID, ID: docID})
	if err != nil {
		return err
	}
	if doc.Status == saga.EInvoiceIssued { // a cancel in doubt
		cmd = commandsv1.IssueEInvoice{Action: "cancel", DocumentId: docID.String(), Kind: doc.Kind, ExternalId: doc.ExternalID, Provider: doc.Provider, Reason: cancelReason}
	}
	return saga.PublishEInvoiceCommand(ctx, tx, now, tenantID, doc.Environment, &cmd, int(attempt))
}

// Sweep sends the commands that are due: a retry whose backoff has passed, a command nobody answered, a cancel nobody answered. A document that has used
// up its automatic attempts is failed. It uses FOR UPDATE SKIP LOCKED, so any number of pods can sweep. It returns how many documents it handled.
func (c *Commands) Sweep(ctx context.Context, limit int) (int, error) {
	now := c.now()
	due, err := sqlc.New(c.Pool).ListDueEInvoices(ctx, sqlc.ListDueEInvoicesParams{Now: now, SilentBefore: now.Add(-ReplyWait), RowLimit: int32(limit)}) //nolint:gosec // small
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range due {
		err := pgxx.InTx(ctx, c.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			q := sqlc.New(tx)
			if _, err := q.LockDueEInvoice(ctx, sqlc.LockDueEInvoiceParams{TenantID: d.TenantID, ID: d.ID, Now: now, SilentBefore: now.Add(-ReplyWait)}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil // another pod has it
				}
				return err
			}
			doc, err := q.LockEInvoiceDocument(ctx, sqlc.LockEInvoiceDocumentParams(d))
			if err != nil {
				return err
			}
			n++
			if doc.Status == saga.EInvoicePending && int(doc.Streak) > len(Backoff) {
				return q.SetEInvoiceFailed(ctx, sqlc.SetEInvoiceFailedParams{ErrorKey: "einvoice.provider.unavailable",
					Error: "no answer from the e-invoice provider after the automatic attempts", Now: now, TenantID: d.TenantID, ID: d.ID})
			}
			return dispatch(ctx, tx, now, d.TenantID, d.ID, doc, "")
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Retry sends a failed document again with the seller's and buyer's data read afresh (the usual reason for a failure is a missing tax id). Lines, amounts,
// number and date stay as they were; the document keeps its UUID, so the integrator can never hold two for one invoice.
func (c *Commands) Retry(ctx context.Context, tenantID, docID uuid.UUID) error {
	var refreshed [2]*saga.Party
	if c.Parties != nil {
		ext, err := sqlc.New(c.Pool).GetEInvoiceExternalCustomer(ctx, sqlc.GetEInvoiceExternalCustomerParams{TenantID: tenantID, ID: docID})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if ext != "" {
			if refreshed[0], refreshed[1], err = c.Parties.Parties(ctx, tenantID, ext); err != nil {
				return errkey.New(codes.Unavailable, "einvoice.provider.unavailable", "could not read the seller and buyer data: "+err.Error())
			}
		}
	}
	return pgxx.InTx(ctx, c.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		doc, err := q.LockEInvoiceDocument(ctx, sqlc.LockEInvoiceDocumentParams{TenantID: tenantID, ID: docID})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if doc.Status != saga.EInvoiceFailed {
			return errkey.New(codes.FailedPrecondition, "einvoice.document.not_failed", "only a failed e-document is retried")
		}
		var cmd commandsv1.IssueEInvoice
		if err := proto.Unmarshal(doc.Request, &cmd); err != nil {
			return err
		}
		if refreshed[0] != nil && cmd.GetDocument() != nil {
			cmd.Document.Seller, cmd.Document.Buyer = refreshed[0].Proto(), refreshed[1].Proto()
		}
		raw, err := proto.Marshal(&cmd)
		if err != nil {
			return err
		}
		now := c.now()
		if err := q.ResetEInvoiceForRetry(ctx, sqlc.ResetEInvoiceForRetryParams{Request: raw, Now: now, TenantID: tenantID, ID: docID}); err != nil {
			return err
		}
		doc.Request, doc.Status = raw, saga.EInvoicePending
		return dispatch(ctx, tx, now, tenantID, docID, doc, "")
	})
}

// Cancel asks the provider to cancel an issued document. The document stays issued until the worker says it is cancelled; a refusal is recorded as the last error.
func (c *Commands) Cancel(ctx context.Context, tenantID, docID uuid.UUID, reason string) error {
	return pgxx.InTx(ctx, c.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		doc, err := q.LockEInvoiceDocument(ctx, sqlc.LockEInvoiceDocumentParams{TenantID: tenantID, ID: docID})
		if errors.Is(err, pgx.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if doc.Status != saga.EInvoiceIssued {
			return errkey.New(codes.FailedPrecondition, "einvoice.document.not_issued", "only an issued e-document can be cancelled")
		}
		now := c.now()
		if err := q.RequestEInvoiceCancel(ctx, sqlc.RequestEInvoiceCancelParams{Now: now, TenantID: tenantID, ID: docID}); err != nil {
			return err
		}
		return dispatch(ctx, tx, now, tenantID, docID, doc, truncate(reason, 300))
	})
}

func notFound() error {
	return errkey.New(codes.NotFound, "einvoice.document.not_found", "e-document not found")
}
