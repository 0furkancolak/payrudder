//go:build integration

package query_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/tenantinfo"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/apply"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/maintenance"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/settings"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/usage/query"
	"github.com/0furkancolak/payrudder/services/aggregator/migrations"
)

type meters map[string]usage.Meter

func (m meters) Get(_ context.Context, _ uuid.UUID, code string) (usage.Meter, error) {
	if v, ok := m[code]; ok {
		return v, nil
	}
	return usage.Meter{}, apply.ErrMeterNotFound
}

// cellsMode runs the fixture's tenant in a cell (its own schema of a second database) instead of the pooled database: ITEST_CELLS=1 go test -tags integration.
func cellsMode() bool { return os.Getenv("ITEST_CELLS") != "" }

type fixture struct {
	t       *testing.T
	handler *apply.Handler
	queries *query.Queries
	// system is the pool the scenarios run direct SQL on: the system role of the pooled database, or in cells mode of the tenant's schema in the cell.
	system   *pgxpool.Pool
	pooled   *pgxpool.Pool
	cell     *testkit.ServiceDB // cells mode
	dir      *testkit.Directory
	offset   int64
	tenant   uuid.UUID
	customer uuid.UUID
	loc      *time.Location
	now      time.Time
}

// newFixture starts Postgres only: the handler is fed records directly, the way the Kafka consumer would.
func newFixture(t *testing.T, tz string) *fixture {
	t.Helper()
	pg := testkit.StartPostgres(t)
	db := testkit.MigratedDB(t, pg, "aggregator", migrations.FS)
	ctx := context.Background()
	system, err := pgxx.NewPool(ctx, db.System)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(system.Close)
	app, err := pgxx.NewPool(ctx, db.App)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatal(err)
	}
	src := settings.NewSourceFunc(func(context.Context, uuid.UUID) (tenantinfo.Settings, error) {
		return tenantinfo.Settings{Timezone: tz, Location: loc, Grace: time.Hour}, nil
	}, time.Minute)
	f := &fixture{t: t, tenant: uuid.New(), customer: uuid.New(), loc: loc, now: time.Now()}
	var appDB pgxx.Beginner = app
	var cells *pgxx.Router
	f.system, f.pooled = system, system
	if cellsMode() {
		cell := testkit.CreateCellDB(t, pg, "aggregator", "c1")
		f.cell, f.dir = &cell, testkit.NewDirectory()
		cellApp, err := pgxx.NewPool(ctx, cell.App)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cellApp.Close)
		cellSystem, err := pgxx.NewPool(ctx, cell.System)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cellSystem.Close)
		if appDB, err = pgxx.NewRouter(app, map[string]*pgxpool.Pool{"c1": cellApp}, f.dir); err != nil {
			t.Fatal(err)
		}
		if cells, err = pgxx.NewRouter(system, map[string]*pgxpool.Pool{"c1": cellSystem}, f.dir); err != nil {
			t.Fatal(err)
		}
		f.switchTenant(f.tenant)
	}
	f.handler = &apply.Handler{
		Pool: system, Cells: cells, Settings: src, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ConsumerGroup: "g", Topic: "t",
		Meters: meters{
			"sum_m":    {Code: "sum_m", Aggregation: usage.Sum},
			"max_m":    {Code: "max_m", Aggregation: usage.Max},
			"unique_m": {Code: "unique_m", Aggregation: usage.Unique, ValueProperty: "user"},
			"last_m":   {Code: "last_m", Aggregation: usage.Last},
		},
		MeterRetryDelays: []time.Duration{time.Millisecond},
	}
	f.queries = query.New(appDB, "g", "t")
	return f
}

// switchTenant makes id the fixture's tenant. In cells mode it is placed in the cell, and f.system, which the scenarios use for direct SQL, becomes the cell's
// system role with the tenant's schema first on its search path, so the same statements read the tenant's own tables.
func (f *fixture) switchTenant(id uuid.UUID) {
	f.t.Helper()
	f.tenant = id
	if f.cell == nil {
		return
	}
	ctx := context.Background()
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"aggregator_app", "aggregator_system"}}
	if _, err := pgxx.ProvisionTenant(ctx, f.cell.Migrator, ts, id, nil); err != nil {
		f.t.Fatal(err)
	}
	f.dir.Place(id, "c1")
	u, err := url.Parse(f.cell.System)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", pgxx.SearchPath(tenantdir.Schema(id)))
	u.RawQuery = q.Encode()
	if f.system, err = pgxx.NewPool(ctx, u.String()); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(f.system.Close)
}

type sent struct {
	meter string
	at    time.Time
	units int64
	late  bool
}

// send applies the events in one batch. An on-time event is ingested a minute after its event time; a late one a week after the end of its period.
func (f *fixture) send(events ...sent) {
	f.t.Helper()
	var recs []*kgo.Record
	for i, e := range events {
		ingested := e.at.Add(time.Minute)
		if e.late {
			_, _, end := period.WindowOf(period.Month, e.at, f.loc)
			ingested = end.Add(7 * 24 * time.Hour)
		}
		pb := &eventsv1.UsageEvent{
			EventId: fmt.Sprintf("e%d-%d", f.offset, i), TenantId: f.tenant.String(), IdempotencyKey: fmt.Sprintf("k%d-%d", f.offset, i), CustomerId: f.customer.String(),
			CustomerExternalId: "cus", MeterCode: e.meter, EventTime: timestamppb.New(e.at), IngestedAt: timestamppb.New(ingested),
			QuantityUnits: e.units, PeriodId: period.PeriodOf(e.at, f.loc), MeterVersion: 1,
		}
		b, err := proto.Marshal(pb)
		if err != nil {
			f.t.Fatal(err)
		}
		recs = append(recs, &kgo.Record{Topic: "t", Partition: 0, Offset: f.offset + int64(i), Value: b})
	}
	if err := f.handler.Apply(context.Background(), 0, recs); err != nil {
		f.t.Fatal(err)
	}
	f.offset += int64(len(events))
}

func (f *fixture) read(meter string, max bool, ids ...string) []string {
	f.t.Helper()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: f.tenant})
	keys := make([]*aggregatorv1.UsageKey, len(ids))
	for i, id := range ids {
		keys[i] = &aggregatorv1.UsageKey{CustomerId: f.customer.String(), MeterCode: meter, PeriodId: id, Max: max}
	}
	b, err := f.queries.UsageBatch(ctx, keys, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(b.Usage) != len(ids) {
		f.t.Fatalf("got %d entries for %d keys", len(b.Usage), len(ids))
	}
	out := make([]string, len(ids))
	for i, u := range b.Usage {
		if u.GetPeriodId() != ids[i] {
			f.t.Fatalf("entry %d is for %q, want %q", i, u.GetPeriodId(), ids[i])
		}
		out[i] = u.GetQuantity()
	}
	return out
}

// expect is what the events add up to in the window of kind w that holds the fixture's now: the sum of the on-time events whose own window is that one, or
// the maximum for a max meter.
func (f *fixture) expect(events []sent, meter string, max bool, w period.Window) string {
	target, _, _ := period.WindowOf(w, f.now, f.loc)
	total := big.NewInt(0)
	for _, e := range events {
		if e.meter != meter || e.late {
			continue
		}
		if id, _, _ := period.WindowOf(w, e.at, f.loc); id != target {
			continue
		}
		if max {
			total = new(big.Int).Set(maxInt(total, big.NewInt(e.units)))
		} else {
			total.Add(total, big.NewInt(e.units))
		}
	}
	return total.String()
}

func maxInt(a, b *big.Int) *big.Int {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

// The hour totals, the month rows and GetUsageBatch agree for every window kind: the hour, day, ISO week, month, year and lifetime window of the same instant each hold
// exactly the on-time events whose own window it is, for a sum meter and a max meter, in the zone of the tenant. Late events are in none, and an event older than the
// hour totals are kept for is only in the windows the month rows answer.
func TestUsageBatchReadsEveryWindow(t *testing.T) {
	for _, tz := range []string{"Europe/Istanbul", "America/New_York", "Pacific/Kiritimati"} {
		t.Run(tz, func(t *testing.T) {
			f := newFixture(t, tz)
			now := f.now
			events := []sent{
				{"sum_m", now.Add(-5 * time.Minute), 7, false},
				{"sum_m", now.Add(-20 * time.Minute), 5, false},
				{"sum_m", now.Add(-3 * time.Hour), 11, false},
				{"sum_m", now.Add(-27 * time.Hour), 13, false},
				{"sum_m", now.Add(-80 * time.Hour), 17, false},
				{"sum_m", now.Add(-30 * 24 * time.Hour), 19, false},
				{"sum_m", now.Add(-2 * time.Hour), 1000, true},
				{"max_m", now.Add(-5 * time.Minute), 4, false},
				{"max_m", now.Add(-50 * time.Minute), 9, false},
				{"max_m", now.Add(-27 * time.Hour), 6, false},
				{"max_m", now.Add(-30 * 24 * time.Hour), 99, false},
			}
			// Two batches, so that an hour row is upserted a second time.
			f.send(events[:5]...)
			f.send(events[5:]...)

			windows := []period.Window{period.Hour, period.Day, period.Week, period.Month, period.Year, period.Lifetime}
			ids := make([]string, len(windows))
			for i, w := range windows {
				ids[i], _, _ = period.WindowOf(w, now, f.loc)
			}
			for _, m := range []struct {
				meter string
				max   bool
			}{{"sum_m", false}, {"max_m", true}} {
				got := f.read(m.meter, m.max, ids...)
				for i, w := range windows {
					if want := f.expect(events, m.meter, m.max, w); got[i] != want {
						t.Errorf("%s %s window %s = %s, want %s", m.meter, w, ids[i], got[i], want)
					}
				}
			}
		})
	}
}

// A max meter read without the flag would be summed: the flag is what makes a window of several hour rows a maximum.
func TestUsageBatchMaxFlag(t *testing.T) {
	f := newFixture(t, "UTC")
	events := []sent{{"max_m", f.now.Add(-3 * time.Hour), 4, false}, {"max_m", f.now.Add(-time.Hour), 9, false}}
	f.send(events...)
	week, _, _ := period.WindowOf(period.Week, f.now, f.loc)
	if got, want := f.read("max_m", true, week)[0], f.expect(events, "max_m", true, period.Week); got != want {
		t.Errorf("max = %s, want %s", got, want)
	}
	if got, want := f.read("max_m", false, week)[0], f.expect(events, "max_m", false, period.Week); got != want {
		t.Errorf("sum = %s, want %s", got, want)
	}
}

func TestUsageBatchRefusesAMalformedWindow(t *testing.T) {
	f := newFixture(t, "UTC")
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: f.tenant})
	for _, id := range []string{"", "2026-13", "2026-W54", "2026-11-31", "forever"} {
		_, err := f.queries.UsageBatch(ctx, []*aggregatorv1.UsageKey{{CustomerId: f.customer.String(), MeterCode: "sum_m", PeriodId: id}}, 0)
		if err == nil {
			t.Errorf("window id %q was accepted", id)
		}
	}
}

// The hours older than the retention are deleted by maintenance, newer ones and the month rows stay; an old event is never written.
func TestMaintenancePrunesOldHours(t *testing.T) {
	f := newFixture(t, "UTC")
	f.send(sent{"sum_m", f.now.Add(-time.Hour), 3, false}, sent{"sum_m", f.now.Add(-60 * 24 * time.Hour), 4, false})
	sys := f.system
	count := func() int {
		var n int
		if err := sys.QueryRow(context.Background(), `SELECT count(*) FROM usage_window_aggregates`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("hour rows after the batch = %d, want 1 (the old event has no hour row)", count())
	}
	// A row that was written while it was young and has aged since.
	if _, err := sys.Exec(context.Background(), `INSERT INTO usage_window_aggregates (tenant_id, customer_id, meter_code, generation, hour_id, quantity, quantity_scale, event_count, updated_at)
		VALUES ($1, $2, 'sum_m', 1, '2001-01-01T00', 1, 0, 1, now())`, f.tenant, f.customer); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatal("the aged row was not inserted")
	}
	n, err := maintenance.PruneWindows(context.Background(), sys, time.Now())
	if err != nil || n != 1 || count() != 1 {
		t.Fatalf("PruneWindows = %d, %v; %d rows left", n, err, count())
	}
	var months int
	if err := sys.QueryRow(context.Background(), `SELECT count(*) FROM usage_aggregates WHERE tenant_id = $1`, f.tenant).Scan(&months); err != nil || months != 2 {
		t.Fatalf("month rows = %d, %v", months, err)
	}
}

// A replay carries the hour totals of the periods it did not rebuild over to the new generation, like the month rows, and a discarded generation loses them.
func TestGenerationCarryOverIncludesHours(t *testing.T) {
	f := newFixture(t, "UTC")
	f.send(sent{"sum_m", f.now.Add(-time.Hour), 3, false})
	sys := f.system
	q := sqlc.New(sys)
	ctx := context.Background()
	hour, _, _ := period.WindowOf(period.Hour, f.now.Add(-time.Hour), f.loc)
	pid := hour[:7]
	gen := func(g int32) (n int) {
		if err := sys.QueryRow(ctx, `SELECT count(*) FROM usage_window_aggregates WHERE tenant_id = $1 AND generation = $2`, f.tenant, g).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The replayed range is another month: the hours of this one are carried.
	if err := q.CopyGenerationWindowUsageOutside(ctx, sqlc.CopyGenerationWindowUsageOutsideParams{TenantID: f.tenant, Source: 1, Target: 2, FromPeriod: "2001-01", ToPeriod: "2001-01"}); err != nil || gen(2) != 1 {
		t.Fatalf("copy outside another range: %d rows, %v", gen(2), err)
	}
	// A replay of this very month rebuilt it itself: nothing of it is carried, and what the target held of it is cleared.
	if err := q.ClearGenerationWindowUsageOutside(ctx, sqlc.ClearGenerationWindowUsageOutsideParams{TenantID: f.tenant, Generation: 2, FromPeriod: pid, ToPeriod: pid}); err != nil || gen(2) != 1 {
		t.Fatalf("clear inside the range removed rows: %d, %v", gen(2), err)
	}
	if err := q.ClearGenerationWindowUsageOutside(ctx, sqlc.ClearGenerationWindowUsageOutsideParams{TenantID: f.tenant, Generation: 2, FromPeriod: "2001-01", ToPeriod: "2001-01"}); err != nil || gen(2) != 0 {
		t.Fatalf("clear outside the range: %d, %v", gen(2), err)
	}
	if err := q.CopyGenerationWindowUsageOutside(ctx, sqlc.CopyGenerationWindowUsageOutsideParams{TenantID: f.tenant, Source: 1, Target: 2, FromPeriod: pid, ToPeriod: pid}); err != nil || gen(2) != 0 {
		t.Fatalf("copy of the rebuilt range: %d, %v", gen(2), err)
	}
	if err := q.DeleteGenerationWindowUsage(ctx, sqlc.DeleteGenerationWindowUsageParams{TenantID: f.tenant, Generation: 1}); err != nil || gen(1) != 0 {
		t.Fatalf("delete generation: %d, %v", gen(1), err)
	}
}
