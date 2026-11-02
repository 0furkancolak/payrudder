// Package migrations embeds the payments_db goose migrations.
package migrations

import (
	"embed"
	"io/fs"
)

// FS holds the SQL migrations.
//
//go:embed *.sql
var FS embed.FS

// RLSExempt are the tables with a tenant_id column that have no row-level security, on purpose: payments_db holds no tenant data beyond ids and only payments_system
// touches it (see the head of 00001_init.sql), so every table with a tenant_id is listed. pgxx.VerifyForcedRLS skips them at startup and in the integration tests, so a
// new tenant table without RLS is refused until it is added here with its reason.
var RLSExempt = []string{"audit_events", "einvoice_accounts", "einvoice_issuances", "manual_receipts", "payment_attempts", "payment_ledger", "provider_account_events", "provider_accounts",
	"provider_events", "provider_objects", "provider_subscriptions"}

//go:embed tenant/*.sql
var tenantFS embed.FS

// Tenant holds the migrations of a tenant's schema in a cell (phase 28, ADR 0018): the same tables as FS, written for a schema instead of public, and the
// shared index of provider notifications that only a cell needs.
func Tenant() fs.FS {
	sub, err := fs.Sub(tenantFS, "tenant")
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return sub
}
