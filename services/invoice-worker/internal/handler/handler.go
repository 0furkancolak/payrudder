// Package handler executes RenderInvoicePdf: render the PDF, store it under a deterministic key, reply with its hash.
package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	commandsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/commands/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/invoice-worker/internal/render"
)

// Store is the object storage the worker needs (pkg/blob.Store satisfies it).
type Store interface {
	Head(ctx context.Context, key string) (blob.Info, error)
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, body []byte, o blob.PutOptions) error
}

// LogoReader reads the tenants' invoice logos from the assets bucket (pkg/blob.Store satisfies it).
type LogoReader interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

const (
	// metaSHA256 is the object metadata key that carries the PDF's hash.
	metaSHA256 = "sha256"
	// tagEnvironment is the object tag the bucket's lifecycle rule matches on.
	tagEnvironment = "environment"
)

// Handler renders invoices.
type Handler struct {
	Store Store
	// Logos is the assets bucket the tenants' invoice logos live in. The worker always sets it; nil renders every
	// invoice without a logo.
	Logos LogoReader
	// Log receives what is kept out of the reply; nil is slog.Default().
	Log *slog.Logger

	locks workerx.KeyedMutex // by object key
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// ObjectKey is {tenant_id}/{invoice_id}/v{invoice_version}.pdf.
func ObjectKey(tenantID string, inv *commandsv1.InvoiceSnapshot) string {
	return fmt.Sprintf("%s/%s/v%d.pdf", tenantID, inv.GetInvoiceId(), inv.GetVersion())
}

func failed(reason string) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PdfFailed{PdfFailed: &commandsv1.PdfFailed{Reason: reason, ErrorKey: errkey.Key("billing.invoice.pdf_failed")}}}
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
	// A command that does not say which environment it is for, or whose invoice number is of the other series, is never rendered: the watermark and the bucket
	// tag follow the environment, so guessing one could print a real invoice as a sandbox one or the reverse. Dead-lettered, not retried.
	environment, err := env.FromProto(cmd.GetEnvironment())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", workerx.ErrPoison, err)
	}
	if !env.NumberMatches(environment, inv.GetNumber()) {
		return nil, fmt.Errorf("%w: invoice number %q is not in the %s series: %w", workerx.ErrPoison, inv.GetNumber(), environment, env.ErrMismatch)
	}
	key := ObjectKey(cmd.GetTenantId(), inv)

	// Attempts of the same step (billing re-issues a silent command with a fresh command id) can arrive together: the
	// first renders, the others find its object. Across pods a duplicate write is harmless, the bytes are identical.
	defer h.locks.Lock(key)()

	info, err := h.Store.Head(ctx, key)
	switch {
	case err == nil && info.Metadata[metaSHA256] != "":
		return rendered(key, info.Metadata[metaSHA256], info.Size), nil
	case err != nil && !errors.Is(err, blob.ErrNotFound):
		return nil, err // storage unavailable: retry
	}

	sandbox := environment.IsSandbox()
	var logo []byte
	// The logo is only read from the command tenant's own prefix.
	if k := inv.GetLogoObjectKey(); k != "" && h.Logos != nil && strings.HasPrefix(k, cmd.GetTenantId()+"/assets/") {
		if b, err := h.Logos.Get(ctx, k); err == nil {
			logo = b
		} else if !errors.Is(err, blob.ErrNotFound) {
			return nil, err
		}
	}
	pdf, err := render.PDF(inv, render.Options{Sandbox: sandbox, Logo: logo})
	if errors.Is(err, render.ErrInvalid) {
		// The detail is for the log; the reply carries a fixed reason and the key.
		h.log().WarnContext(ctx, "invoice PDF not rendered", "invoice", inv.GetInvoiceId(), "version", inv.GetVersion(), "err", err)
		return failed("the invoice cannot be rendered"), nil
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pdf)
	hash := hex.EncodeToString(sum[:])
	if err := h.Store.Put(ctx, key, pdf, blob.PutOptions{
		ContentType: "application/pdf", Metadata: map[string]string{metaSHA256: hash},
		// The bucket's lifecycle rule expires objects tagged environment=sandbox after 30 days.
		Tags: map[string]string{tagEnvironment: environment.String()},
	}); err != nil {
		return nil, err
	}
	return rendered(key, hash, int64(len(pdf))), nil
}

func rendered(key, hash string, size int64) *commandsv1.ReplyEnvelope {
	return &commandsv1.ReplyEnvelope{Reply: &commandsv1.ReplyEnvelope_PdfRendered{PdfRendered: &commandsv1.PdfRendered{ObjectKey: key, Sha256: hash, SizeBytes: size}}}
}
