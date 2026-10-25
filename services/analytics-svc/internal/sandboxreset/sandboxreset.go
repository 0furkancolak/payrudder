// Package sandboxreset deletes a sandbox tenant's rows from analytics-svc's ClickHouse tables (phase 45 E). ClickHouse deletes are mutations: they run in the
// background unless told to wait, and this waits (mutations_sync = 1) so that "done" means done. Every statement is written out; none is built from a name.
package sandboxreset

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

type table struct {
	name   string
	count  string
	delete string
}

// tables is everything analytics-svc keeps for a tenant. usage_hourly is filled by a materialized view from usage_events and has to be emptied itself.
var tables = []table{
	{"usage_events", `SELECT count() FROM usage_events WHERE tenant_id = ?`, `ALTER TABLE usage_events DELETE WHERE tenant_id = ? SETTINGS mutations_sync = 1`},
	{"usage_hourly", `SELECT count() FROM usage_hourly WHERE tenant_id = ?`, `ALTER TABLE usage_hourly DELETE WHERE tenant_id = ? SETTINGS mutations_sync = 1`},
	{"limit_events", `SELECT count() FROM limit_events WHERE tenant_id = ?`, `ALTER TABLE limit_events DELETE WHERE tenant_id = ? SETTINGS mutations_sync = 1`},
	{"invoice_facts", `SELECT count() FROM invoice_facts WHERE tenant_id = ?`, `ALTER TABLE invoice_facts DELETE WHERE tenant_id = ? SETTINGS mutations_sync = 1`},
}

// Tables are the tables a reset deletes from.
func Tables() []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		out = append(out, t.name)
	}
	return out
}

// Resetter deletes through a ClickHouse connection.
type Resetter struct{ conn driver.Conn }

// New returns a Resetter.
func New(conn driver.Conn) *Resetter { return &Resetter{conn: conn} }

// Reset deletes the tenant's rows and returns how many there were, by table (tables with none are left out).
func (r *Resetter) Reset(ctx context.Context, tenantID uuid.UUID) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range tables {
		var n uint64
		if err := r.conn.QueryRow(ctx, t.count, tenantID).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		if err := r.conn.Exec(ctx, t.delete, tenantID); err != nil {
			return nil, err
		}
		out[t.name] = int64(n) //nolint:gosec // a row count
	}
	return out, nil
}
