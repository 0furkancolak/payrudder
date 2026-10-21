package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/0furkancolak/quotify/pkg/secretbox"
	"github.com/0furkancolak/quotify/services/payments-worker/internal/store/sqlc"
)

// EInvoiceAccounts is the table of a tenant's e-invoice integrator accounts. Settings are sealed; the box is bound to tenant and environment.
type EInvoiceAccounts struct {
	DB  *pgxpool.Pool
	Box *secretbox.Box
}

// EInvoiceAccountInfo is an account without its secrets.
type EInvoiceAccountInfo struct {
	Environment    string    `json:"environment"`
	Provider       string    `json:"provider"`
	Mode           string    `json:"mode"`
	HasCredentials bool      `json:"has_credentials"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func einvoiceAAD(tenant uuid.UUID, env string) []byte {
	return []byte(fmt.Sprintf("%s|einvoice|%s", tenant, env))
}

// Put stores (or replaces) the tenant's account for an environment.
func (a EInvoiceAccounts) Put(ctx context.Context, tenant uuid.UUID, env, provider, mode string, settings []byte) error {
	sealed, err := a.Box.Seal(settings, einvoiceAAD(tenant, env))
	if err != nil {
		return err
	}
	return sqlc.New(a.DB).UpsertEInvoiceAccount(ctx, sqlc.UpsertEInvoiceAccountParams{TenantID: tenant, Environment: env, Provider: provider, Mode: mode, SettingsEnc: sealed})
}

// Delete removes an account. It reports whether there was one.
func (a EInvoiceAccounts) Delete(ctx context.Context, tenant uuid.UUID, env string) (bool, error) {
	n, err := sqlc.New(a.DB).DeleteEInvoiceAccount(ctx, sqlc.DeleteEInvoiceAccountParams{TenantID: tenant, Environment: env})
	return n == 1, err
}

// List returns a tenant's accounts without secrets.
func (a EInvoiceAccounts) List(ctx context.Context, tenant uuid.UUID) ([]EInvoiceAccountInfo, error) {
	rows, err := sqlc.New(a.DB).ListEInvoiceAccounts(ctx, tenant)
	if err != nil {
		return nil, err
	}
	out := make([]EInvoiceAccountInfo, len(rows))
	for i, r := range rows {
		out[i] = EInvoiceAccountInfo{Environment: r.Environment, Provider: r.Provider, Mode: r.Mode, HasCredentials: true, UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}

// Settings opens the account of an environment; ErrNoAccount when there is none.
func (a EInvoiceAccounts) Settings(ctx context.Context, tenant uuid.UUID, env string) (provider string, settings []byte, err error) {
	row, err := sqlc.New(a.DB).GetEInvoiceAccount(ctx, sqlc.GetEInvoiceAccountParams{TenantID: tenant, Environment: env})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNoAccount
	}
	if err != nil {
		return "", nil, err
	}
	settings, err = a.Box.Open(row.SettingsEnc, einvoiceAAD(tenant, env))
	return row.Provider, settings, err
}

// EInvoiceIssuances is the idempotency record of issued documents.
type EInvoiceIssuances struct{ DB *pgxpool.Pool }

// Issuance is the record of one document.
type Issuance struct {
	DocumentID                                uuid.UUID
	Environment, Provider, Kind, DocumentType string
	Status, LegalNumber, ExternalID           string
	XMLKey, PDFKey                            string
}

// Begin records that the integrator is about to be called for the document. It reports whether the row was new.
func (i EInvoiceIssuances) Begin(ctx context.Context, doc, tenant uuid.UUID, env, provider, kind, docType string) (bool, error) {
	n, err := sqlc.New(i.DB).BeginEInvoiceIssuance(ctx, sqlc.BeginEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant, Environment: env, Provider: provider, Kind: kind, DocumentType: docType})
	return n == 1, err
}

// Get returns the record of a document, if any.
func (i EInvoiceIssuances) Get(ctx context.Context, doc, tenant uuid.UUID) (Issuance, bool, error) {
	r, err := sqlc.New(i.DB).GetEInvoiceIssuance(ctx, sqlc.GetEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant})
	if errors.Is(err, pgx.ErrNoRows) {
		return Issuance{}, false, nil
	}
	if err != nil {
		return Issuance{}, false, err
	}
	return Issuance{DocumentID: r.DocumentID, Environment: r.Environment, Provider: r.Provider, Kind: r.Kind, DocumentType: r.DocumentType, Status: r.Status,
		LegalNumber: r.LegalNumber, ExternalID: r.ExternalID, XMLKey: r.XmlKey, PDFKey: r.PdfKey}, true, nil
}

// Complete records the integrator's answer: from now on the document is issued under that legal number, for good.
func (i EInvoiceIssuances) Complete(ctx context.Context, doc, tenant uuid.UUID, legal, external, xmlKey, pdfKey string) error {
	return sqlc.New(i.DB).CompleteEInvoiceIssuance(ctx, sqlc.CompleteEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant, LegalNumber: &legal, ExternalID: external,
		XmlKey: nilIfEmpty(xmlKey), PdfKey: nilIfEmpty(pdfKey)})
}

// Cancel marks an issued document cancelled.
func (i EInvoiceIssuances) Cancel(ctx context.Context, doc, tenant uuid.UUID) error {
	return sqlc.New(i.DB).CancelEInvoiceIssuance(ctx, sqlc.CancelEInvoiceIssuanceParams{DocumentID: doc, TenantID: tenant})
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
