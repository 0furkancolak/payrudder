// Package migrations embeds the ClickHouse migrations of analytics-svc.
package migrations

import "embed"

// FS holds the SQL migrations.
//
//go:embed *.sql
var FS embed.FS
