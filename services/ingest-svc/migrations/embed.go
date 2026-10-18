// Package migrations embeds the ingest_db goose migrations.
package migrations

import "embed"

// FS holds the SQL migrations.
//
//go:embed *.sql
var FS embed.FS
