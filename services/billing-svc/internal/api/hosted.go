package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

const defaultLinkDays = 90

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CreateInvoiceLink implements billingv1.BillingServiceServer.
func (s *Server) CreateInvoiceLink(ctx context.Context, req *billingv1.CreateInvoiceLinkRequest) (*billingv1.CreateInvoiceLinkResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	days := req.GetExpiresInDays()
	switch {
	case days == 0:
		days = defaultLinkDays
	case days < 0 || days > 365:
		return nil, errkey.New(codes.InvalidArgument, "billing.invoice_link.bad_expiry", "expires_in_days must be 1..365")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var invStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1`, id).Scan(&invStatus); err != nil {
			return err
		}
		if invStatus == "draft" {
			return errkey.New(codes.FailedPrecondition, "billing.invoice_link.draft_invoice", "a draft invoice has no number yet and cannot be shared")
		}
		_, err := tx.Exec(ctx, `INSERT INTO invoice_links (tenant_id, id, invoice_id, token_hash, expires_at) VALUES ($1,$2,$3,$4,$5)`,
			ti.ID, uuid.New(), id, tokenHash(token), expires)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, status.Error(codes.NotFound, "invoice not found")
	case status.Code(err) == codes.FailedPrecondition:
		return nil, err
	case err != nil:
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.CreateInvoiceLinkResponse{Token: token, ExpireTime: timestamppb.New(expires)}, nil
}

// RevokeInvoiceLinks implements billingv1.BillingServiceServer.
func (s *Server) RevokeInvoiceLinks(ctx context.Context, req *billingv1.RevokeInvoiceLinksRequest) (*billingv1.RevokeInvoiceLinksResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	var n int64
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invoices WHERE id = $1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		tag, err := tx.Exec(ctx, `UPDATE invoice_links SET revoked_at = now() WHERE invoice_id = $1 AND revoked_at IS NULL`, id)
		n = tag.RowsAffected()
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.RevokeInvoiceLinksResponse{Revoked: int32(n)}, nil //nolint:gosec // a handful of links
}

// errNoLink is what a visitor gets for a token that is unknown, expired or revoked: the three look the same.
var errNoLink = errkey.New(codes.NotFound, "billing.invoice_link.not_found", "this link does not exist or has expired")

// linkedInvoice resolves a token to its tenant and invoice, or errNoLink.
func (s *Server) linkedInvoice(ctx context.Context, token string) (tenantID, invoiceID uuid.UUID, expires time.Time, err error) {
	if len(token) < 20 || len(token) > 100 {
		return uuid.Nil, uuid.Nil, time.Time{}, errNoLink
	}
	err = s.system.QueryRow(ctx, `SELECT tenant_id, invoice_id, expires_at FROM invoice_links WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		tokenHash(token)).Scan(&tenantID, &invoiceID, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, time.Time{}, errNoLink
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, time.Time{}, status.Error(codes.Internal, "internal error")
	}
	return tenantID, invoiceID, expires, nil
}

// GetHostedInvoice implements billingv1.HostedInvoiceServiceServer.
func (s *Server) GetHostedInvoice(ctx context.Context, req *billingv1.GetHostedInvoiceRequest) (*billingv1.GetHostedInvoiceResponse, error) {
	_, invoiceID, expires, err := s.linkedInvoice(ctx, req.GetToken())
	if err != nil {
		return nil, err
	}
	resp := &billingv1.GetHostedInvoiceResponse{ExpireTime: timestamppb.New(expires)}
	err = pgxx.InTx(ctx, s.system, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		if resp.Invoice, err = loadInvoice(ctx, tx, invoiceID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(snapshot->>'tenant_name', ''), COALESCE(snapshot->>'customer_name', ''), COALESCE((snapshot->>'currency_exponent')::int, 2)
			FROM invoices WHERE id = $1`, invoiceID).Scan(&resp.SellerName, &resp.CustomerName, &resp.CurrencyExponent)
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if resp.Invoice.Status == "draft" {
		return nil, errNoLink
	}
	resp.PdfAvailable = resp.Invoice.PdfObjectKey != "" && s.pdfs != nil
	resp.Invoice.PdfObjectKey = "" // the visitor gets a link, not a storage key
	return resp, nil
}

// GetHostedInvoicePdfUrl implements billingv1.HostedInvoiceServiceServer.
func (s *Server) GetHostedInvoicePdfUrl(ctx context.Context, req *billingv1.GetHostedInvoicePdfUrlRequest) (*billingv1.GetHostedInvoicePdfUrlResponse, error) {
	_, invoiceID, _, err := s.linkedInvoice(ctx, req.GetToken())
	if err != nil {
		return nil, err
	}
	if s.pdfs == nil {
		return nil, status.Error(codes.Unimplemented, "object storage is not configured")
	}
	var key *string
	if err := s.system.QueryRow(ctx, `SELECT pdf_object_key FROM invoices WHERE id = $1`, invoiceID).Scan(&key); err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if key == nil || *key == "" {
		return nil, errkey.New(codes.FailedPrecondition, "billing.invoice.pdf_not_ready", "the invoice PDF is not rendered yet")
	}
	url, err := s.pdfs.PresignGet(ctx, *key, PDFLinkTTL)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not sign the download link")
	}
	return &billingv1.GetHostedInvoicePdfUrlResponse{Url: url, ExpireTime: timestamppb.New(time.Now().Add(PDFLinkTTL))}, nil
}
