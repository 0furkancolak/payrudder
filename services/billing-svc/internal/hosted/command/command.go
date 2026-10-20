// Package command creates and revokes the public links of invoices. The rules for links live here.
package command

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

const (
	defaultLinkDays = 90
	maxLinkDays     = 365
)

// TokenHash is what is stored of a link token: the token itself is only ever shown once.
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CreateLink makes a link to an invoice that works for expiresInDays (0 means the default) and returns the token and the expiry.
func CreateLink(ctx context.Context, app *pgxpool.Pool, tenantID, invoiceID uuid.UUID, expiresInDays int32) (string, time.Time, error) {
	days := expiresInDays
	switch {
	case days == 0:
		days = defaultLinkDays
	case days < 0 || days > maxLinkDays:
		return "", time.Time{}, errkey.New(codes.InvalidArgument, "billing.invoice_link.bad_expiry", "expires_in_days must be 1..365")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, status.Error(codes.Internal, "internal error")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		invStatus, err := q.GetInvoiceStatus(ctx, invoiceID)
		if err != nil {
			return err
		}
		if invStatus == "draft" {
			return errkey.New(codes.FailedPrecondition, "billing.invoice_link.draft_invoice", "a draft invoice has no number yet and cannot be shared")
		}
		return q.InsertInvoiceLink(ctx, sqlc.InsertInvoiceLinkParams{TenantID: tenantID, ID: uuid.New(), InvoiceID: invoiceID, TokenHash: TokenHash(token), ExpiresAt: expires})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", time.Time{}, status.Error(codes.NotFound, "invoice not found")
	case status.Code(err) == codes.FailedPrecondition:
		return "", time.Time{}, err
	case err != nil:
		return "", time.Time{}, status.Error(codes.Internal, "internal error")
	}
	return token, expires, nil
}

// RevokeLinks revokes every active link of an invoice and returns how many there were.
func RevokeLinks(ctx context.Context, app *pgxpool.Pool, tenantID, invoiceID uuid.UUID) (int64, error) {
	var n int64
	err := pgxx.InTenantTx(ctx, app, tenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		exists, err := q.InvoiceExists(ctx, invoiceID)
		if err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		n, err = q.RevokeInvoiceLinks(ctx, invoiceID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return 0, status.Error(codes.Internal, "internal error")
	}
	return n, nil
}
