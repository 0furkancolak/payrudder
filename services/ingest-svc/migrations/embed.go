// Package migrations embeds the ingest_db goose migrations.
package migrations

import (
	"embed"
	"io/fs"
)

// FS holds the SQL migrations.
//
//go:embed *.sql
var FS embed.FS

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
