// Package handler executes RenderInvoicePdf: render the PDF, store it under a deterministic key, reply with its hash.
package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	commandsv1 "github.com/0furkancolak/quotify/gen/go/quotify/commands/v1"
	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/workerx"
	"github.com/0furkancolak/quotify/services/invoice-worker/internal/render"
)

// Store is the object storage the worker needs (pkg/blob.Store satisfies it).
type Store interface {
	Head(ctx context.Context, key string) (blob.Info, error)
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, body []byte, o blob.PutOptions) error
}

// Handler renders invoices.
type Handler struct {
	Store Store

	locks sync.Map // object key -> *sync.Mutex
}

// ObjectKey is {tenant_id}/{invoice_id}/v{invoice_version}.pdf.
func ObjectKey(tenantID string, inv *commandsv1.InvoiceSnapshot) string {
	return fmt.Sprintf("%s/%s/v%d.pdf", tenantID, inv.GetInvoiceId(), inv.GetVersion())
}

func failed(reason string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PdfFailed{PdfFailed: &commandsv1.PdfFailed{Reason: reason}}}
}

// Handle implements workerx.Handler.
//
// Idempotency: the object key is deterministic and the object carries its sha256 as metadata. If the object exists with a
// hash, the reply is built from it and nothing is rendered or written; a crash between the write and the reply is
// repaired the same way.
func (h *Handler) Handle(ctx context.Context, cmd *commandsv1.CommandEnvelope) (*commandsv1.ReplyEnvelope, error) {
	r := cmd.GetRenderInvoicePdf()
	if r == nil || r.GetInvoice() == nil {
		return nil, fmt.Errorf("%w: not a render command", workerx.ErrPoison)
	}
	inv := r.GetInvoice()
	key := ObjectKey(cmd.GetTenantId(), inv)

	// Attempts of the same step (billing re-issues a silent command with a fresh command id) can arrive together: the
	// first renders, the others find its object. Across pods a duplicate write is harmless, the bytes are identical.
	mu, _ := h.locks.LoadOrStore(key, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer func() {
		mu.(*sync.Mutex).Unlock()
		h.locks.Delete(key)
	}()

	info, err := h.Store.Head(ctx, key)
	switch {
	case err == nil && info.Metadata["sha256"] != "":
		return rendered(key, info.Metadata["sha256"], info.Size), nil
	case err != nil && !errors.Is(err, blob.ErrNotFound):
		return nil, err // storage unavailable: retry
	}

	sandbox := cmd.GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX
	var logo []byte
	if k := inv.GetLogoObjectKey(); k != "" {
		if b, err := h.Store.Get(ctx, k); err == nil {
			logo = b
		} else if !errors.Is(err, blob.ErrNotFound) {
			return nil, err
		}
	}
	pdf, err := render.PDF(inv, render.Options{Sandbox: sandbox, Logo: logo})
	if errors.Is(err, render.ErrInvalid) {
		return failed(err.Error()), nil
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pdf)
	hash := hex.EncodeToString(sum[:])
	env := "live"
	if sandbox {
		env = "sandbox"
	}
	if err := h.Store.Put(ctx, key, pdf, blob.PutOptions{
		ContentType: "application/pdf", Metadata: map[string]string{"sha256": hash},
		// The bucket's lifecycle rule expires objects tagged environment=sandbox after 30 days.
		Tags: map[string]string{"environment": env},
	}); err != nil {
		return nil, err
	}
	return rendered(key, hash, int64(len(pdf))), nil
}

func rendered(key, hash string, size int64) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PdfRendered{PdfRendered: &commandsv1.PdfRendered{ObjectKey: key, Sha256: hash, SizeBytes: size}}}
}
