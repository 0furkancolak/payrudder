//go:build integration

package itest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/apply"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/maintenance"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

// Phase 28 stage 3: the usage consumer in cells mode. One batch spans tenants in the pooled database and in a cell; it is split by placement, each cell tenant in a
// transaction of its own in its own schema, the offset moves last. These scenarios feed the handler directly (the way the Kafka consumer would), so they need only
// Postgres; the Kafka suite, TestAggregator, runs in this mode with ITEST_CELLS=1.

// cellEnv is a pooled database, a cell and a handler over both.
type cellEnv struct {
	t        *testing.T
	pg       testkit.Postgres
	db, cell testkit.ServiceDB
	pooled   *pgxpool.Pool // aggregator_system in the pooled database
	cellSys  *pgxpool.Pool // aggregator_system in the cell
	dir      *testkit.Directory
	router   *pgxx.Router
	handler  *apply.Handler
	src      *settings.Source
	inCell   map[uuid.UUID]bool
	next     int64 // the next offset of partition 0 the scenarios produce
	now      time.Time
}

var cellMeters = fakeMeters{
	"sum_m":    {Code: "sum_m", Aggregation: usage.Sum},
	"count_m":  {Code: "count_m", Aggregation: usage.Count},
	"max_m":    {Code: "max_m", Aggregation: usage.Max},
	"unique_m": {Code: "unique_m", Aggregation: usage.Unique, ValueProperty: "user"},
	"last_m":   {Code: "last_m", Aggregation: usage.Last},
}

func newCellEnv(t *testing.T) *cellEnv {
	t.Helper()
	ctx := testkit.Ctx(t)
	e := &cellEnv{t: t, pg: testkit.StartPostgres(t), dir: testkit.NewDirectory(), inCell: map[uuid.UUID]bool{}, now: time.Now().UTC()}
	e.db = testkit.MigratedDB(t, e.pg, "aggregator", migrations.FS)
	e.cell = testkit.CreateCellDB(t, e.pg, "aggregator", "c1")
	open := func(dsn string) *pgxpool.Pool {
		p, err := pgxx.NewPool(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	e.pooled, e.cellSys = open(e.db.System), open(e.cell.System)
	var err error
	if e.router, err = pgxx.NewRouter(e.pooled, map[string]*pgxpool.Pool{"c1": e.cellSys}, e.dir); err != nil {
		t.Fatal(err)
	}
	src := settings.NewSourceFunc(func(context.Context, uuid.UUID) (tenantinfo.Settings, error) {
		return tenantinfo.Settings{Timezone: "UTC", Location: time.UTC, Grace: time.Hour}, nil
	}, time.Minute)
	e.src = src
	e.handler = e.newHandler("g")
	return e
}

// newHandler is a consumer of the environment in a group of its own.
func (e *cellEnv) newHandler(group string) *apply.Handler {
	return &apply.Handler{
		Pool: e.pooled, Cells: e.router, Meters: cellMeters, Settings: e.src, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ConsumerGroup: group, Topic: "t", MeterRetryDelays: []time.Duration{time.Millisecond},
	}
}

// tenant returns a new tenant; in a cell when cell is set (its schema is built).
func (e *cellEnv) tenant(cell bool) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	e.place(id, cell)
	return id
}

func (e *cellEnv) place(id uuid.UUID, cell bool) {
	e.t.Helper()
	if !cell {
		return
	}
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
	if _, err := pgxx.ProvisionTenant(testkit.Ctx(e.t), e.cell.Migrator, ts, id, nil); err != nil {
		e.t.Fatal(err)
	}
	e.dir.Place(id, "c1")
	e.inCell[id] = true
}

// tenants returns n new tenants in ascending order of id, the order the consumer visits cell tenants in.
func (e *cellEnv) tenants(cell bool, n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	for _, id := range ids {
		e.place(id, cell)
	}
	return ids
}

// at runs fn as the system role where the tenant's data is.
func (e *cellEnv) at(tn uuid.UUID, fn func(ctx context.Context, q querier) error) {
	e.t.Helper()
	ctx := context.Background()
	var err error
	if e.inCell[tn] {
		err = pgxx.InTenantSchemaTx(ctx, e.cellSys, tn, func(tx pgx.Tx) error { return fn(ctx, tx) })
	} else {
		err = fn(ctx, e.pooled)
	}
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *cellEnv) count(tn uuid.UUID, table string) int {
	e.t.Helper()
	var n int
	e.at(tn, func(ctx context.Context, q querier) error {
		// The table names are the fixed ones of these scenarios.
		return q.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tn).Scan(&n)
	})
	return n
}

// offset is the consumer's committed position on partition 0 (-1: none).
func (e *cellEnv) offset(group string) int64 {
	e.t.Helper()
	var n int64
	err := e.pooled.QueryRow(context.Background(), `SELECT COALESCE((SELECT next_offset FROM consumer_offsets WHERE consumer_group = $1 AND topic = 't' AND partition = 0), -1)`, group).Scan(&n)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// records turns events into records of partition 0 from the offset on.
func records(t *testing.T, from int64, evs []*eventsv1.UsageEvent) []*kgo.Record {
	t.Helper()
	recs := make([]*kgo.Record, len(evs))
	for i, x := range evs {
		b, err := marshal(x)
		if err != nil {
			t.Fatal(err)
		}
		recs[i] = &kgo.Record{Topic: "t", Partition: 0, Offset: from + int64(i), Value: b}
	}
	return recs
}

// mk builds a usage event of the scenarios' customer; user is the value of a UNIQUE meter.
func mk(id string, tn uuid.UUID, meter string, units int64, user string, at, ingested time.Time) *eventsv1.UsageEvent {
	e := ev(id, tn, scenarioCustomer, meter, units, 0, period.PeriodOf(at, time.UTC), ingested)
	e.EventTime = timestamppb.New(at)
	if user != "" {
		e.Properties = map[string]string{"user": user}
	}
	return e
}

var scenarioCustomer = uuid.MustParse("c0ffee00-0000-4000-8000-000000000001")

// stream is one tenant's events: every aggregation, on-time and late, a UNIQUE value that repeats and one that comes late, and one event published twice. The same
// stream goes to every tenant of a scenario, so that their rows can be compared.
func (e *cellEnv) stream(tn uuid.UUID) []*eventsv1.UsageEvent {
	cur := time.Date(e.now.Year(), e.now.Month(), 1, 0, 0, 0, 0, time.UTC)
	prevAt := cur.Add(-5 * 24 * time.Hour)
	lateIngested := e.now
	at := func(i int) time.Time { return e.now.Add(-time.Duration(i) * 7 * time.Minute) }
	var out []*eventsv1.UsageEvent
	for i := 0; i < 12; i++ {
		out = append(out, mk(fmt.Sprintf("S%02d", i), tn, "sum_m", int64(i+1), "", at(i), at(i).Add(time.Second)))
	}
	for i := 0; i < 3; i++ {
		out = append(out, mk(fmt.Sprintf("C%02d", i), tn, "count_m", 1, "", at(i), at(i).Add(time.Second)))
		out = append(out, mk(fmt.Sprintf("M%02d", i), tn, "max_m", int64(5+i*2-i/2*3), "", at(i), at(i).Add(time.Second)))
		out = append(out, mk(fmt.Sprintf("L%02d", i), tn, "last_m", int64(100+i), "", at(2-i), at(2-i).Add(time.Second)))
	}
	for i, u := range []string{"u0", "u1", "u2", "u0", "u1"} {
		out = append(out, mk(fmt.Sprintf("U%02d", i), tn, "unique_m", 0, u, at(i), at(i).Add(time.Second)))
	}
	// Late: October's events ingested after the cutoff. u1 is seen late first, then u3; a LAST event that is late is dropped.
	for i, u := range []string{"u1", "u3", "u3"} {
		out = append(out, mk(fmt.Sprintf("UL%02d", i), tn, "unique_m", 0, u, prevAt.Add(time.Duration(i)*time.Hour), lateIngested))
	}
	out = append(out, mk("SL00", tn, "sum_m", 40, "", prevAt, lateIngested), mk("SL01", tn, "sum_m", 2, "", prevAt.Add(time.Hour), lateIngested))
	out = append(out, mk("LL00", tn, "last_m", 7, "", prevAt, lateIngested))
	// The relay published S00 twice.
	out = append(out, mk("S00", tn, "sum_m", 1, "", at(0), at(0).Add(time.Second)))
	return out
}

// interleave puts the streams of the tenants in one batch, event by event, so that every tenant's events are spread across it.
func (e *cellEnv) interleave(tenants ...uuid.UUID) []*eventsv1.UsageEvent {
	streams := make([][]*eventsv1.UsageEvent, len(tenants))
	n := 0
	for i, tn := range tenants {
		streams[i] = e.stream(tn)
		n = max(n, len(streams[i]))
	}
	var out []*eventsv1.UsageEvent
	for k := 0; k < n; k++ {
		for i := range tenants {
			if k < len(streams[i]) {
				out = append(out, streams[i][k])
			}
		}
	}
	return out
}

// dump is everything the tenant has, as lines that do not name the tenant: equal streams give equal dumps in any place.
func (e *cellEnv) dump(tn uuid.UUID) []string {
	e.t.Helper()
	var lines []string
	e.at(tn, func(ctx context.Context, q querier) error {
		rows, err := q.Query(ctx, `
			SELECT concat_ws('|', 'usage', period_id, customer_id, meter_code, generation, quantity, quantity_scale, event_count, coalesce(last_event_id, ''), coalesce(last_at::text, '')) FROM usage_aggregates WHERE tenant_id = $1
			UNION ALL SELECT concat_ws('|', 'late', billed_period_id, customer_id, meter_code, origin_period_id, generation, quantity, event_count) FROM late_usage WHERE tenant_id = $1
			UNION ALL SELECT concat_ws('|', 'hour', hour_id, customer_id, meter_code, generation, quantity, event_count, coalesce(last_event_id, '')) FROM usage_window_aggregates WHERE tenant_id = $1
			UNION ALL SELECT concat_ws('|', 'values', period_id, customer_id, meter_code, generation, encode(value_hash, 'hex'), billed_period_id) FROM usage_distinct_values WHERE tenant_id = $1
			UNION ALL SELECT concat_ws('|', 'hours', hour_id, customer_id, meter_code, generation, encode(value_hash, 'hex')) FROM usage_distinct_hours WHERE tenant_id = $1
			UNION ALL SELECT concat_ws('|', 'inbox', period_id, event_id, generation) FROM processed_events WHERE tenant_id = $1
			ORDER BY 1`, tn)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l string
			if err := rows.Scan(&l); err != nil {
				return err
			}
			lines = append(lines, l)
		}
		return rows.Err()
	})
	return lines
}

func same(a, b []string) bool { return slices.Equal(a, b) }

// A batch of events of tenants in the pooled database and in a cell: every tenant ends up with the rows it would have had alone and in either mode, in its own place
// and nowhere else, and each cell tenant was written in a transaction of its own.
func TestConsumerSplitsABatchPerPlacement(t *testing.T) {
	e := newCellEnv(t)
	ctx := context.Background()
	pooled := e.tenants(false, 1)[0]
	cells := e.tenants(true, 3)
	all := append([]uuid.UUID{pooled}, cells...)
	batch := e.interleave(all...)
	if err := e.handler.Apply(ctx, 0, records(t, 0, batch)); err != nil {
		t.Fatal(err)
	}
	if got := e.offset("g"); got != int64(len(batch)) {
		t.Fatalf("offset = %d, want %d", got, len(batch))
	}

	ref := e.dump(pooled)
	for _, kind := range []string{"usage|", "late|", "hour|", "values|", "hours|", "inbox|"} {
		if !slices.ContainsFunc(ref, func(l string) bool { return strings.HasPrefix(l, kind) }) {
			t.Fatalf("the pooled tenant has no %q row: the stream proves nothing about it\n%s", kind, strings.Join(ref, "\n"))
		}
	}
	for _, tn := range cells {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("cell tenant %s differs from the pooled tenant:\n got %s\nwant %s", tn, strings.Join(got, "\n"), strings.Join(ref, "\n"))
		}
	}
	// The sums are the stream's: S00..S11 = 78 over 12 events (the duplicate counts once), the late ones billed in this month.
	want := fmt.Sprintf("usage|%s|%s|sum_m|1|78|0|12||", period.PeriodOf(e.now, time.UTC), scenarioCustomer)
	if !slices.Contains(ref, want) {
		t.Fatalf("no row %q in\n%s", want, strings.Join(ref, "\n"))
	}

	// Each place holds its own tenants' rows and no one else's, in every table.
	tables := []string{"usage_aggregates", "late_usage", "usage_window_aggregates", "usage_distinct_values", "usage_distinct_hours", "processed_events", "period_settings"}
	for _, tn := range all {
		for _, tb := range tables {
			var others int
			e.at(tn, func(ctx context.Context, q querier) error {
				return q.QueryRow(ctx, `SELECT count(*) FROM `+tb+` WHERE tenant_id <> $1`, tn).Scan(&others)
			})
			if others != 0 {
				t.Errorf("%s of tenant %s's place holds %d rows of other tenants", tb, tn, others)
			}
		}
	}
	for _, tn := range cells {
		for _, tb := range tables {
			var n int
			if err := e.pooled.QueryRow(ctx, `SELECT count(*) FROM `+tb+` WHERE tenant_id = $1`, tn).Scan(&n); err != nil || n != 0 {
				t.Errorf("the pooled %s holds %d rows of the cell tenant %s (%v)", tb, n, tn, err)
			}
		}
	}

	// Transactions: the rows of one cell tenant were all written by one transaction, no two tenants share it, and the pooled tenant's rows share theirs with the offset.
	xmins := func(tn uuid.UUID) map[int64]bool {
		out := map[int64]bool{}
		for _, tb := range tables[:6] {
			e.at(tn, func(ctx context.Context, q querier) error {
				rows, err := q.Query(ctx, `SELECT DISTINCT xmin::text::bigint FROM `+tb+` WHERE tenant_id = $1`, tn)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var x int64
					if err := rows.Scan(&x); err != nil {
						return err
					}
					out[x] = true
				}
				return rows.Err()
			})
		}
		return out
	}
	seen := map[int64]uuid.UUID{}
	for _, tn := range all {
		x := xmins(tn)
		if len(x) != 1 {
			t.Fatalf("the rows of tenant %s were written by %d transactions, want 1: %v", tn, len(x), x)
		}
		for id := range x {
			if other, dup := seen[id]; dup {
				t.Fatalf("tenants %s and %s were written by the same transaction %d", other, tn, id)
			}
			seen[id] = tn
		}
	}
	var offsetTx int64
	if err := e.pooled.QueryRow(ctx, `SELECT xmin::text::bigint FROM consumer_offsets WHERE consumer_group = 'g'`).Scan(&offsetTx); err != nil {
		t.Fatal(err)
	}
	if seen[offsetTx] != pooled {
		t.Fatalf("the offset was written by transaction %d, the pooled tenant's is %v", offsetTx, seen)
	}
}

// One tenant of the batch fails in the middle of its own transaction: the tenants before and after it are durable, its own rows are not (rolled back with its inbox),
// the pooled tenants' transaction does not run and the offset does not move. The batch delivered again is applied exactly once everywhere.
func TestConsumerBatchWithAFailureInTheMiddle(t *testing.T) {
	e := newCellEnv(t)
	ctx := context.Background()
	ref := newReference(t, e)

	pooled := e.tenants(false, 1)[0]
	cells := e.tenants(true, 3) // ascending: the consumer visits a, b, c in this order
	a, b, c := cells[0], cells[1], cells[2]
	batch := e.interleave(pooled, a, b, c)
	recs := records(t, 100, batch)

	// b's late usage cannot be written: its transaction fails after the inbox, the aggregates, the hour totals and the value sets of the batch were.
	cellDDL(t, e, `ALTER TABLE `+tenantdir.Schema(b)+`.late_usage RENAME TO late_usage_broken`)
	err := e.handler.Apply(ctx, 0, recs)
	if err == nil {
		t.Fatal("the batch was applied although a tenant's transaction failed")
	}
	if !strings.Contains(err.Error(), b.String()) || strings.Contains(err.Error(), a.String()) || strings.Contains(err.Error(), c.String()) {
		t.Fatalf("the error should name tenant %s and only it: %v", b, err)
	}
	if got := e.offset("g"); got != -1 {
		t.Fatalf("offset = %d after a failed batch, want none", got)
	}
	for _, tn := range []uuid.UUID{a, c} {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("tenant %s (visited around the failing one) is not durable:\n got %s\nwant %s", tn, strings.Join(got, "\n"), strings.Join(ref, "\n"))
		}
	}
	for _, tb := range []string{"processed_events", "usage_aggregates", "usage_window_aggregates", "usage_distinct_values", "usage_distinct_hours"} {
		if n := e.count(b, tb); n != 0 {
			t.Errorf("%s of the failing tenant has %d rows: its transaction was not rolled back as a whole", tb, n)
		}
	}
	if d := e.dump(pooled); len(d) != 0 {
		t.Fatalf("the pooled tenant was written although the batch failed (its transaction carries the offset):\n%s", strings.Join(d, "\n"))
	}

	// Delivered again once the tenant can be written: exactly once everywhere, and the offset moves.
	cellDDL(t, e, `ALTER TABLE `+tenantdir.Schema(b)+`.late_usage_broken RENAME TO late_usage`)
	if err := e.handler.Apply(ctx, 0, recs); err != nil {
		t.Fatal(err)
	}
	for _, tn := range []uuid.UUID{pooled, a, b, c} {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("tenant %s after the redelivery:\n got %s\nwant %s", tn, strings.Join(got, "\n"), strings.Join(ref, "\n"))
		}
	}
	if got := e.offset("g"); got != 100+int64(len(batch)) {
		t.Fatalf("offset = %d, want %d", got, 100+len(batch))
	}
	// And once more (a zombie, a redelivery after the commit): nothing changes.
	if err := e.handler.Apply(ctx, 0, recs); err != nil {
		t.Fatal(err)
	}
	for _, tn := range []uuid.UUID{pooled, a, b, c} {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("tenant %s counted a redelivered batch again", tn)
		}
	}
}

// newReference is the dump a tenant of the scenarios has after one application of the stream, taken in a throwaway tenant of the same environment.
func newReference(t *testing.T, e *cellEnv) []string {
	t.Helper()
	tn := e.tenants(true, 1)[0]
	// In a group of its own, so that the scenarios' group starts without an offset.
	if err := e.newHandler("ref").Apply(context.Background(), 0, records(t, 0, e.stream(tn))); err != nil {
		t.Fatal(err)
	}
	ref := e.dump(tn)
	if len(ref) < 20 {
		t.Fatalf("reference dump has only %d lines", len(ref))
	}
	return ref
}

// cellDDL runs a statement as the owner of the cell database.
func cellDDL(t *testing.T, e *cellEnv, stmt string) {
	t.Helper()
	ctx := testkit.Ctx(t)
	conn, err := pgx.Connect(ctx, e.cell.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, stmt); err != nil {
		t.Fatal(err)
	}
}

// The process dies after every cell tenant is durable and before the offset moves: the batch is delivered again, and the tenants in the cell (already applied) and the
// pooled one (not yet) both end up with exactly one application.
func TestConsumerCrashBetweenTheSplitsAndTheOffset(t *testing.T) {
	e := newCellEnv(t)
	ctx := context.Background()
	ref := newReference(t, e)
	pooled := e.tenants(false, 2)
	cells := e.tenants(true, 2)
	all := append(append([]uuid.UUID{}, pooled...), cells...)
	recs := records(t, 0, e.interleave(all...))

	var crashes atomic.Int32
	e.handler.AfterSplits = func() error {
		if crashes.Add(1) == 1 {
			return errors.New("simulated crash before the offset moves")
		}
		return nil
	}
	if err := e.handler.Apply(ctx, 0, recs); err == nil {
		t.Fatal("the crash hook did not stop the batch")
	}
	if got := e.offset("g"); got != -1 {
		t.Fatalf("offset = %d after the crash, want none", got)
	}
	for _, tn := range cells {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("cell tenant %s is not durable before the offset moves", tn)
		}
	}
	for _, tn := range pooled {
		if n := e.count(tn, "processed_events"); n != 0 {
			t.Fatalf("pooled tenant %s was written before the offset's transaction: %d inbox rows", tn, n)
		}
	}
	if err := e.handler.Apply(ctx, 0, recs); err != nil {
		t.Fatal(err)
	}
	for _, tn := range all {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("tenant %s after the redelivery:\n got %s\nwant %s", tn, strings.Join(got, "\n"), strings.Join(ref, "\n"))
		}
	}
	if got := e.offset("g"); got != int64(len(recs)) {
		t.Fatalf("offset = %d, want %d", got, len(recs))
	}
}

// A tenant that is moving makes the batch wait: nothing of it, or of the tenants around it, is written and the offset stays, so the move is neither skipped nor raced.
func TestConsumerWaitsForATenantThatIsMoving(t *testing.T) {
	e := newCellEnv(t)
	ctx := context.Background()
	ref := newReference(t, e)
	pooled := e.tenants(false, 1)[0]
	cells := e.tenants(true, 2)
	all := append([]uuid.UUID{pooled}, cells...)
	recs := records(t, 0, e.interleave(all...))

	e.dir.Set(tenantdir.Placement{TenantID: cells[0], Cell: "c1", Status: tenantdir.StatusMoving})
	err := e.handler.Apply(ctx, 0, recs)
	if !errors.Is(err, tenantdir.ErrNotServing) {
		t.Fatalf("Apply = %v, want the tenant's move to stop the batch", err)
	}
	if got := e.offset("g"); got != -1 {
		t.Fatalf("offset = %d while a tenant moves", got)
	}
	for _, tn := range all {
		if n := e.count(tn, "processed_events"); n != 0 {
			t.Fatalf("tenant %s was written (%d inbox rows) while another tenant of the batch was moving", tn, n)
		}
	}
	e.dir.Place(cells[0], "c1")
	if err := e.handler.Apply(ctx, 0, recs); err != nil {
		t.Fatal(err)
	}
	for _, tn := range all {
		if got := e.dump(tn); !same(got, ref) {
			t.Fatalf("tenant %s after the move:\n got %s\nwant %s", tn, strings.Join(got, "\n"), strings.Join(ref, "\n"))
		}
	}
}

// Dual writing and replay work in a tenant's schema: while a generation catches up, the live batch writes both; a replay handler fills the new one from the log,
// idempotently (the inbox is per generation), and the two end equal.
func TestGenerationsAndReplayInACell(t *testing.T) {
	e := newCellEnv(t)
	ctx := context.Background()
	tn := e.tenants(true, 1)[0]
	first := e.stream(tn)
	if err := e.handler.Apply(ctx, 0, records(t, 0, first)); err != nil {
		t.Fatal(err)
	}
	// A replay opened: generation 2 is catching up beside the active 1.
	e.at(tn, func(ctx context.Context, q querier) error {
		_, err := q.Exec(ctx, `INSERT INTO aggregate_generations (tenant_id, generation, status) VALUES ($1, 1, 'active'), ($1, 2, 'catching_up')`, tn)
		return err
	})
	cur := time.Date(e.now.Year(), e.now.Month(), 1, 0, 0, 0, 0, time.UTC)
	second := []*eventsv1.UsageEvent{
		mk("X00", tn, "sum_m", 5, "", e.now, e.now.Add(time.Second)),
		mk("X01", tn, "sum_m", 6, "", e.now, e.now.Add(time.Second)),
		mk("X02", tn, "unique_m", 0, "u9", e.now, e.now.Add(time.Second)),
	}
	if err := e.handler.Apply(ctx, 0, records(t, int64(len(first)), second)); err != nil {
		t.Fatal(err)
	}
	gens := func() map[int32]string {
		out := map[int32]string{}
		e.at(tn, func(ctx context.Context, q querier) error {
			rows, err := q.Query(ctx, `SELECT generation, quantity::text || '/' || event_count FROM usage_aggregates WHERE tenant_id = $1 AND meter_code = 'sum_m' AND period_id = $2`, tn, period.PeriodOf(cur, time.UTC))
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var g int32
				var v string
				if err := rows.Scan(&g, &v); err != nil {
					return err
				}
				out[g] = v
			}
			return rows.Err()
		})
		return out
	}
	if got := gens(); got[1] != "89/14" || got[2] != "11/2" {
		t.Fatalf("after the live dual write: %v, want generation 1 = 78+11 over 14 events and generation 2 = 11 over 2", got)
	}
	// The replay handler reads the first batch into generation 2, twice (a restart): the inbox of generation 2 makes the second read a no-op.
	replayer := e.newHandler("replay-1")
	replayer.Replay = &apply.Replay{Tenant: tn, Generation: 2, FromPeriod: "2000-01", ToPeriod: "2999-12"}
	for range 2 {
		if err := replayer.Apply(ctx, 0, records(t, 0, first)); err != nil {
			t.Fatal(err)
		}
	}
	if got := gens(); got[1] != "89/14" || got[2] != "89/14" {
		t.Fatalf("after the replay: %v, want both generations equal", got)
	}
	if got := e.offset("replay-1"); got != int64(len(first)) {
		t.Fatalf("replay offset = %d, want %d", got, len(first))
	}
}

// Maintenance visits every serving tenant of the cell once, drops its expired inbox partitions and old hour rows in its own schema, leaves a tenant that is moving
// alone until it serves again, and does the pooled database separately.
func TestMaintenanceVisitsEveryCellTenantOnce(t *testing.T) {
	e := newCellEnv(t)
	ctx := context.Background()
	pooled := e.tenants(false, 1)[0]
	cells := e.tenants(true, 3)
	all := append([]uuid.UUID{pooled}, cells...)
	if err := e.handler.Apply(ctx, 0, records(t, 0, e.interleave(all...))); err != nil {
		t.Fatal(err)
	}
	for _, tn := range cells {
		e.at(tn, func(ctx context.Context, q querier) error {
			_, err := q.Exec(ctx, `INSERT INTO usage_distinct_hours (tenant_id, customer_id, meter_code, generation, hour_id, value_hash) VALUES ($1, $2, 'unique_m', 1, '2001-01-01T00', sha256('z'::bytea))`, tn, scenarioCustomer)
			return err
		})
	}
	oldHours := func(tn uuid.UUID) int {
		var n int
		e.at(tn, func(ctx context.Context, q querier) error {
			return q.QueryRow(ctx, `SELECT count(*) FROM usage_distinct_hours WHERE hour_id = '2001-01-01T00'`).Scan(&n)
		})
		return n
	}
	partition := "processed_events_" + strings.ReplaceAll(period.PeriodOf(e.now, time.UTC), "-", "_")
	exists := func(tn uuid.UUID) bool {
		var reg *string
		if err := e.cellSys.QueryRow(ctx, `SELECT to_regclass($1)::text`, tenantdir.Schema(tn)+"."+partition).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		return reg != nil
	}
	for _, tn := range cells {
		if !exists(tn) {
			t.Fatalf("tenant %s has no inbox partition to expire", tn)
		}
	}
	later := e.now.AddDate(0, 6, 0)

	// One tenant is moving: it is not touched, the others are, once each.
	moving := cells[1]
	e.dir.Set(tenantdir.Placement{TenantID: moving, Cell: "c1", Status: tenantdir.StatusMoving})
	dropped, pruned, err := maintenance.OnceCells(ctx, e.router, later)
	if err != nil || pruned == 0 {
		t.Fatalf("run: pruned %d, %v", pruned, err)
	}
	for _, tn := range cells {
		n := 0
		for _, d := range dropped {
			if strings.HasPrefix(d, tn.String()+".") && strings.HasSuffix(d, partition) {
				n++
			}
		}
		switch want := map[bool]int{true: 0, false: 1}[tn == moving]; {
		case n != want:
			t.Errorf("tenant %s: partition dropped %d times, want %d (moving: %v)", tn, n, want, tn == moving)
		case exists(tn) != (tn == moving):
			t.Errorf("tenant %s: partition exists = %v after the run", tn, exists(tn))
		case oldHours(tn) != map[bool]int{true: 1, false: 0}[tn == moving]:
			t.Errorf("tenant %s: %d old hour sets left after the run (moving: %v)", tn, oldHours(tn), tn == moving)
		}
	}
	// It serves again: the next run takes it, and only it.
	e.dir.Place(moving, "c1")
	dropped, pruned, err = maintenance.OnceCells(ctx, e.router, later)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) == 0 || pruned == 0 || exists(moving) || oldHours(moving) != 0 {
		t.Fatalf("the run after the move: dropped %v, pruned %d, partition exists %v, old hour sets %d", dropped, pruned, exists(moving), oldHours(moving))
	}
	for _, d := range dropped {
		if !strings.HasPrefix(d, moving.String()+".") {
			t.Errorf("the run after the move touched %s", d)
		}
	}
	// The pooled tenant's partitions are the pooled run's.
	if names, err := maintenance.Once(ctx, e.pooled, later); err != nil || !slices.Contains(names, partition) {
		t.Fatalf("pooled maintenance dropped %v, %v", names, err)
	}
	if again, _, err := maintenance.OnceCells(ctx, e.router, later); err != nil || len(again) != 0 {
		t.Fatalf("a second cell run dropped %v, %v", again, err)
	}
}

// A tenant moves from the pooled database into a cell and back with every row of the aggregator's tables (the partitioned inbox and the generated period columns
// included), and the consumer, which follows the directory, goes on counting it exactly once in either place: an event delivered again after each move changes nothing.
func TestMoveLosesNoAggregatorRow(t *testing.T) {
	e := newCellEnv(t)
	ctx := testkit.Ctx(t)
	tn := e.tenants(false, 1)[0]
	stream := e.stream(tn)
	if err := e.handler.Apply(ctx, 0, records(t, 0, stream)); err != nil {
		t.Fatal(err)
	}
	want := e.dump(tn)
	if len(want) < 20 {
		t.Fatalf("only %d rows to move", len(want))
	}
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
	pooled, cell := pgxx.Endpoint{MigratorDSN: e.db.Migrator}, pgxx.Endpoint{Cell: "c1", MigratorDSN: e.cell.Migrator}
	move := func(from, to pgxx.Endpoint) {
		t.Helper()
		m := pgxx.Move{TenantID: tn, From: from, To: to, Schemas: ts}
		if err := m.Fence(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Copy(ctx); err != nil {
			t.Fatal(err)
		}
	}

	move(pooled, cell)
	e.dir.Place(tn, "c1")
	e.inCell[tn] = true
	if got := e.dump(tn); !same(got, want) {
		t.Fatalf("after the move into the cell:\n got %s\nwant %s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The consumer follows: the same events again change nothing, new ones land in the schema.
	if err := e.handler.Apply(ctx, 0, records(t, 100, stream)); err != nil {
		t.Fatal(err)
	}
	if got := e.dump(tn); !same(got, want) {
		t.Fatal("an event counted again after the move into the cell")
	}
	extra := []*eventsv1.UsageEvent{mk("N00", tn, "sum_m", 1000, "", e.now, e.now.Add(time.Second))}
	if err := e.handler.Apply(ctx, 0, records(t, 200, extra)); err != nil {
		t.Fatal(err)
	}
	want = e.dump(tn)

	move(cell, pooled)
	e.dir.Place(tn, "")
	e.inCell[tn] = false
	if got := e.dump(tn); !same(got, want) {
		t.Fatalf("after the move back:\n got %s\nwant %s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if err := e.handler.Apply(ctx, 0, records(t, 300, stream)); err != nil {
		t.Fatal(err)
	}
	if got := e.dump(tn); !same(got, want) {
		t.Fatal("an event counted again after the move back")
	}
}
