// Package migrations embeds the billing_db goose migrations.
package migrations

import (
	"embed"
	"io/fs"
)

// FS holds the SQL migrations.
//
//go:embed *.sql
var FS embed.FS

// RLSExempt are the tables with a tenant_id column that have no row-level security, on purpose: pgxx.VerifyForcedRLS skips them at startup and in the integration
// tests, so a new tenant table without RLS is refused until it is added here with its reason. invoice_number_seq is only billing_system's (the number is taken under a
// lock at finalize) and outbox is the relay's. (expense_inboxes has its policy since 00006; billing_system, which BYPASSRLS, looks its tokens up without a tenant.)
var RLSExempt = []string{"invoice_number_seq", "outbox"}

//go:embed tenant/*.sql
var tenantFS embed.FS

// Tenant holds the migrations of a tenant's schema in a cell (phase 28, ADR 0018): the same tables as FS, written for a schema instead of public.
func Tenant() fs.FS {
	sub, err := fs.Sub(tenantFS, "tenant")
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return sub
}
