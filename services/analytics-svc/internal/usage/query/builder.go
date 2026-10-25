// Package query builds analytics-svc's ClickHouse queries. A Builder can only be created for one tenant, and every query
// it produces puts `tenant_id = ?` first in the WHERE clause with the tenant as the first argument; there is no way to
// pass SQL in. That predicate is also the first column of every table's ORDER BY, so it doubles as the primary-key prefix.
package query

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Builder builds queries for one tenant.
type Builder struct {
	tenant uuid.UUID
	all    bool
}

// ErrNoTenant means a query was requested without a tenant.
var ErrNoTenant = errors.New("query: a tenant is required")

// ForTenant returns the builder of a tenant.
func ForTenant(id uuid.UUID) (Builder, error) {
	if id == uuid.Nil {
		return Builder{}, ErrNoTenant
	}
	return Builder{tenant: id}, nil
}

// AllTenants returns the builder of cross-tenant operator statistics. The caller proves it is an operator; queries of
// this builder never return rows of individual customers.
func AllTenants(operator bool) (Builder, error) {
	if !operator {
		return Builder{}, errors.New("query: cross-tenant queries are for operators")
	}
	return Builder{all: true}, nil
}

// Query is SQL with positional arguments.
type Query struct {
	SQL  string
	Args []any
}

// Granularity of a usage series.
type Granularity int

const (
	Hour Granularity = iota
	Day
)

// UsageFilter selects a usage series.
type UsageFilter struct {
	Meter       string
	Customer    uuid.UUID // uuid.Nil = every customer
	From, To    time.Time // [From, To)
	Granularity Granularity
	// Timezone buckets days in the tenant's time zone (IANA name); "" means UTC.
	Timezone string
	// Exact reads the deduplicated events instead of the hourly rollup.
	Exact bool
}

// MaxExactRange is the longest range an exact query accepts: FINAL is expensive.
const MaxExactRange = 24 * time.Hour

// Usage builds the usage series query.
func (b Builder) Usage(f UsageFilter) (Query, error) {
	if b.all {
		return Query{}, ErrNoTenant
	}
	if f.Meter == "" {
		return Query{}, errors.New("query: meter is required")
	}
	if !f.From.Before(f.To) {
		return Query{}, errors.New("query: from must be before to")
	}
	tz := f.Timezone
	if tz == "" {
		tz = "UTC"
	}
	args := []any{b.tenant, f.Meter}
	where := "tenant_id = ? AND meter_code = ?"
	if f.Customer != uuid.Nil {
		where += " AND customer_id = ?"
		args = append(args, f.Customer)
	}
	var bucket string
	switch f.Granularity {
	case Hour:
		bucket = "toStartOfHour(%s, 'UTC')"
	case Day:
		bucket = "toStartOfDay(%s, '" + sanitizeTZ(tz) + "')"
	default:
		return Query{}, errors.New("query: unknown granularity")
	}
	if f.Exact {
		if f.To.Sub(f.From) > MaxExactRange {
			return Query{}, fmt.Errorf("query: exact queries cover at most %s", MaxExactRange)
		}
		args = append(args, f.From, f.To)
		return Query{
			SQL: fmt.Sprintf(`SELECT %s AS bucket, sum(quantity_units) AS units, any(quantity_scale) AS scale, count() AS events
				FROM usage_events FINAL WHERE %s AND event_time >= ? AND event_time < ?
				GROUP BY bucket ORDER BY bucket`, fmt.Sprintf(bucket, "event_time"), where),
			Args: args,
		}, nil
	}
	args = append(args, f.From, f.To)
	return Query{
		SQL: fmt.Sprintf(`SELECT %s AS bucket, sum(quantity_units_sum) AS units, any(quantity_scale) AS scale, sum(event_count) AS events
			FROM usage_hourly WHERE %s AND hour >= ? AND hour < ?
			GROUP BY bucket ORDER BY bucket`, fmt.Sprintf(bucket, "hour"), where),
		Args: args,
	}, nil
}

// sanitizeTZ keeps a time zone name safe to inline (ClickHouse needs a literal): letters, digits, '/', '_', '+', '-'.
func sanitizeTZ(tz string) string {
	for _, r := range tz {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '/', r == '_', r == '+', r == '-':
		default:
			return "UTC"
		}
	}
	return tz
}

// TopCustomers ranks customers by usage of a meter in a billing period (deduplicated events).
func (b Builder) TopCustomers(meter, period string, limit int) (Query, error) {
	if b.all {
		return Query{}, ErrNoTenant
	}
	if meter == "" || period == "" {
		return Query{}, errors.New("query: meter and period are required")
	}
	if limit < 1 || limit > 100 {
		return Query{}, errors.New("query: limit must be 1..100")
	}
	return Query{
		SQL: `SELECT customer_id, any(customer_external_id) AS external_id, sum(quantity_units) AS units, any(quantity_scale) AS scale, count() AS events
			FROM usage_events FINAL WHERE tenant_id = ? AND meter_code = ? AND period_id = ?
			GROUP BY customer_id ORDER BY units DESC, customer_id LIMIT ?`,
		Args: []any{b.tenant, meter, period, uint64(limit)}, //nolint:gosec // 1..100
	}, nil
}

// IngestStats reports per-tenant ingest figures over the last window. With a tenant builder it covers that tenant; the
// operator builder covers every tenant.
func (b Builder) IngestStats(window time.Duration, now time.Time) (Query, error) {
	if window < time.Second || window > 24*time.Hour {
		return Query{}, errors.New("query: window must be between 1 s and 24 h")
	}
	since := now.Add(-window)
	const cols = `tenant_id, count() AS events,
		countIf(ingested_at - event_time > 86400) AS late,
		count() - uniqExact(event_id) AS duplicates`
	if b.all {
		return Query{SQL: `SELECT ` + cols + ` FROM usage_events WHERE ingested_at >= ? GROUP BY tenant_id ORDER BY events DESC LIMIT 1000`, Args: []any{since}}, nil
	}
	return Query{SQL: `SELECT ` + cols + ` FROM usage_events WHERE tenant_id = ? AND ingested_at >= ? GROUP BY tenant_id`, Args: []any{b.tenant, since}}, nil
}
