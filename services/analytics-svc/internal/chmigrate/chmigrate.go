// Package chmigrate is a tiny migration runner for ClickHouse: each embedded file runs once and is recorded in
// schema_migrations. Files are applied in name order; a file holds statements ended by a semicolon at the end of a line.
package chmigrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Run applies the pending migrations.
func Run(ctx context.Context, conn driver.Conn, fsys fs.FS) error {
	if err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version String, applied_at DateTime) ENGINE = MergeTree ORDER BY version`); err != nil {
		return fmt.Errorf("chmigrate: create schema_migrations: %w", err)
	}
	applied := map[string]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	_ = rows.Close()

	entries, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		if applied[name] {
			continue
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		for i, stmt := range Statements(string(raw)) {
			if err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("chmigrate: %s statement %d: %w", name, i+1, err)
			}
		}
		if err := conn.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, name, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// Statements splits a migration file into statements (semicolon at the end of a line), dropping comment-only lines.
func Statements(sql string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			if s := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cur.String()), ";")); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// Options builds connection options from the usual settings.
func Options(addr, database, user, password string) *clickhouse.Options {
	return &clickhouse.Options{
		Addr: []string{addr}, Auth: clickhouse.Auth{Database: database, Username: user, Password: password},
		DialTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, MaxOpenConns: 8, MaxIdleConns: 4, ConnMaxLifetime: time.Hour,
		Settings: clickhouse.Settings{"async_insert": 0, "date_time_input_format": "best_effort"},
	}
}
