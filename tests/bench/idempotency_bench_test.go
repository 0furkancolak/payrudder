//go:build bench

// Package bench holds the measurements behind design decisions (docs/adr/0014-idempotency-key-retention.md).
//
// TestIdempotencyRetention compares the two ways to keep ingest-svc's dedup table bounded: month partitions dropped when
// they expire (what the migration does) against one plain table pruned by batched DELETEs. It runs against the compose
// Postgres in a scratch database and prints one line per measurement:
//
//	QUOTIFY_BENCH_ROWS=3000000 go test -tags bench -run TestIdempotencyRetention -v ./bench/
package bench

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

const ddlCommon = `(
  tenant_id        uuid        NOT NULL,
  idempotency_key  text        NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
  event_id         text        NOT NULL,
  payload_hash     bytea       NOT NULL,
  created_at       timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, idempotency_key, created_at)
)`

type variant struct {
	name  string
	setup func(ctx context.Context, db *pgxpool.Pool, months []time.Time) error
	// retire removes the oldest month's rows and reports how many statements it took.
	retire func(ctx context.Context, db *pgxpool.Pool, oldest, next time.Time) (statements int, err error)
	table  string
}

func monthStart(t time.Time, add int) time.Time {
	return time.Date(t.Year(), t.Month()+time.Month(add), 1, 0, 0, 0, 0, time.UTC)
}

var variants = []variant{
	{
		name: "partitioned", table: "idem_p",
		setup: func(ctx context.Context, db *pgxpool.Pool, months []time.Time) error {
			if _, err := db.Exec(ctx, `CREATE TABLE idem_p `+ddlCommon+` PARTITION BY RANGE (created_at)`); err != nil {
				return err
			}
			for i, m := range months {
				if _, err := db.Exec(ctx, fmt.Sprintf(`CREATE TABLE idem_p_%d PARTITION OF idem_p FOR VALUES FROM ('%s') TO ('%s')`,
					i, m.Format(time.RFC3339), monthStart(m, 1).Format(time.RFC3339))); err != nil {
					return err
				}
			}
			return nil
		},
		retire: func(ctx context.Context, db *pgxpool.Pool, _, _ time.Time) (int, error) {
			_, err := db.Exec(ctx, `DROP TABLE idem_p_0`)
			return 1, err
		},
	},
	{
		name: "unpartitioned+batched DELETE", table: "idem_u",
		setup: func(ctx context.Context, db *pgxpool.Pool, _ []time.Time) error {
			if _, err := db.Exec(ctx, `CREATE TABLE idem_u `+ddlCommon); err != nil {
				return err
			}
			_, err := db.Exec(ctx, `CREATE INDEX idem_u_created ON idem_u (created_at)`)
			return err
		},
		retire: func(ctx context.Context, db *pgxpool.Pool, oldest, next time.Time) (int, error) {
			n := 0
			for {
				tag, err := db.Exec(ctx, `DELETE FROM idem_u WHERE (tenant_id, idempotency_key, created_at) IN
					(SELECT tenant_id, idempotency_key, created_at FROM idem_u WHERE created_at >= $1 AND created_at < $2 LIMIT 10000)`, oldest, next)
				if err != nil {
					return n, err
				}
				n++
				if tag.RowsAffected() == 0 {
					return n, nil
				}
			}
		},
	},
}

// insertBatch is ingest's hot path: look up the batch's keys, then insert the new ones (ON CONFLICT DO NOTHING).
func insertBatch(ctx context.Context, db *pgxpool.Pool, table string, tenant string, seq *atomic.Int64, at time.Time, n int) error {
	keys := make([]string, n)
	ids := make([]string, n)
	hashes := make([][]byte, n)
	ats := make([]time.Time, n)
	for i := range keys {
		v := seq.Add(1)
		keys[i], ids[i], hashes[i], ats[i] = fmt.Sprintf("k-%d", v), fmt.Sprintf("ev-%d", v), []byte{1, 2, 3, 4}, at
	}
	if _, err := db.Exec(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1::uuid AND idempotency_key = ANY($2) AND created_at = ANY($3)`, tenant, keys, ats[:1]); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `INSERT INTO `+table+` (tenant_id, idempotency_key, event_id, payload_hash, created_at)
		SELECT $1::uuid, k, e, h, $5::timestamptz FROM unnest($2::text[], $3::text[], $4::bytea[]) AS t(k, e, h) ON CONFLICT DO NOTHING`, tenant, keys, ids, hashes, at)
	return err
}

func quantiles(d []time.Duration) (p50, p99 time.Duration) {
	if len(d) == 0 {
		return 0, 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)/2], d[len(d)*99/100]
}

func TestIdempotencyRetention(t *testing.T) {
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, envOr("QUOTIFY_BENCH_DSN", fmt.Sprintf("postgres://postgres:postgres_local@localhost:%s/postgres", envOr("QUOTIFY_PG_HOST_PORT", "42432"))))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	db := "idem_bench"
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") }()
	cfg := admin.Config().Copy()
	cfg.Database = db
	pool, err := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:%d/%s", cfg.User, cfg.Password, cfg.Host, cfg.Port, db))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	rows := envInt("QUOTIFY_BENCH_ROWS", 3000000)
	const batch = 500
	now := time.Now().UTC()
	months := []time.Time{monthStart(now, -2), monthStart(now, -1), monthStart(now, 0), monthStart(now, 1)}
	tenants := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"}

	for _, v := range variants {
		if err := v.setup(ctx, pool, months); err != nil {
			t.Fatalf("%s setup: %v", v.name, err)
		}
		var seq atomic.Int64

		// 1. Fill three months, four writers.
		start := time.Now()
		var wg sync.WaitGroup
		perMonth := rows / 3 / batch
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := w; i < perMonth*3; i += 4 {
					at := months[i%3].Add(time.Duration(i) * time.Second)
					if err := insertBatch(ctx, pool, v.table, tenants[i%len(tenants)], &seq, at, batch); err != nil {
						t.Error(err)
						return
					}
				}
			}()
		}
		wg.Wait()
		fill := time.Since(start)
		var size string
		_ = pool.QueryRow(ctx, `SELECT pg_size_pretty(COALESCE(sum(pg_total_relation_size(oid)), 0)::bigint) FROM pg_class WHERE relkind = 'r' AND relname ~ ('^' || $1 || '(_[0-9]+)?$')`, v.table).Scan(&size)
		t.Logf("%-30s fill: %d rows in %s = %.0f rows/s, %s on disk", v.name, perMonth*3*batch, fill.Round(time.Millisecond), float64(perMonth*3*batch)/fill.Seconds(), size)

		// 2. Steady state: current-month inserts only, 10 s, four writers.
		measure := func(d time.Duration, during func()) (time.Duration, time.Duration, float64) {
			var mu sync.Mutex
			var lat []time.Duration
			deadline := time.Now().Add(d)
			var n atomic.Int64
			var wg sync.WaitGroup
			for w := 0; w < 4; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; time.Now().Before(deadline); i++ {
						s := time.Now()
						if err := insertBatch(ctx, pool, v.table, tenants[(w+i)%len(tenants)], &seq, months[2].Add(time.Duration(i)*time.Millisecond), batch); err != nil {
							t.Error(err)
							return
						}
						mu.Lock()
						lat = append(lat, time.Since(s))
						mu.Unlock()
						n.Add(batch)
					}
				}()
			}
			if during != nil {
				go during()
			}
			wg.Wait()
			p50, p99 := quantiles(lat)
			return p50, p99, float64(n.Load()) / d.Seconds()
		}
		p50, p99, rate := measure(10*time.Second, nil)
		t.Logf("%-30s steady insert: %.0f rows/s, batch p50 %s p99 %s", v.name, rate, p50.Round(time.Microsecond), p99.Round(time.Microsecond))

		// 3. Retire the oldest month while inserts continue.
		var retireTook time.Duration
		var statements int
		done := make(chan struct{})
		p50, p99, rate = measure(20*time.Second, func() {
			s := time.Now()
			statements, err = v.retire(ctx, pool, months[0], months[1])
			retireTook = time.Since(s)
			close(done)
		})
		<-done
		if err != nil {
			t.Fatalf("%s retire: %v", v.name, err)
		}
		var dead int64
		_ = pool.QueryRow(ctx, `SELECT COALESCE(sum(n_dead_tup), 0)::bigint FROM pg_stat_user_tables WHERE relname LIKE $1`, v.table+"%").Scan(&dead)
		_ = pool.QueryRow(ctx, `SELECT pg_size_pretty(COALESCE(sum(pg_total_relation_size(oid)), 0)::bigint) FROM pg_class WHERE relkind = 'r' AND relname ~ ('^' || $1 || '(_[0-9]+)?$')`, v.table).Scan(&size)
		t.Logf("%-30s retire oldest month: %s in %d statement(s); inserts meanwhile %.0f rows/s, batch p50 %s p99 %s; %d dead tuples, %s on disk (before VACUUM)",
			v.name, retireTook.Round(time.Millisecond), statements, rate, p50.Round(time.Microsecond), p99.Round(time.Microsecond), dead, size)
		s := time.Now()
		if _, err := pool.Exec(ctx, `VACUUM `+v.table); err != nil {
			t.Logf("vacuum: %v", err)
		}
		_ = pool.QueryRow(ctx, `SELECT pg_size_pretty(COALESCE(sum(pg_total_relation_size(oid)), 0)::bigint) FROM pg_class WHERE relkind = 'r' AND relname ~ ('^' || $1 || '(_[0-9]+)?$')`, v.table).Scan(&size)
		t.Logf("%-30s VACUUM: %s, %s on disk afterwards", v.name, time.Since(s).Round(time.Millisecond), size)
	}
}
