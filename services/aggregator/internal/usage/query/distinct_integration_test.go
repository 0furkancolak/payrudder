//go:build integration

package query_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/usage"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/maintenance"
	"github.com/0furkancolak/payrudder/services/aggregator/internal/store/sqlc"
)

// uev is one event of a UNIQUE or LAST meter, with everything the tests vary: its id (the LAST tie-break), the user the UNIQUE meter counts, and when it was ingested.
type uev struct {
	meter    string
	id       string
	at       time.Time
	ingested time.Time // zero: a minute after at, which is before the cutoff of the period
	units    int64
	user     string
}

// lateAt is an ingestion time after the cutoff of the event's period: a week after the period ended.
func (f *fixture) lateAt(at time.Time, plus time.Duration) time.Time {
	_, _, end := period.WindowOf(period.Month, at, f.loc)
	return end.Add(plus)
}

func (f *fixture) sendEvents(events ...uev) {
	f.t.Helper()
	var recs []*kgo.Record
	for i, e := range events {
		ingested := e.ingested
		if ingested.IsZero() {
			ingested = e.at.Add(time.Minute)
		}
		props := map[string]string{}
		if e.user != "" {
			props["user"] = e.user
		}
		pb := &eventsv1.UsageEvent{
			EventId: e.id, TenantId: f.tenant.String(), IdempotencyKey: "k-" + e.id, CustomerId: f.customer.String(), CustomerExternalId: "cus",
			MeterCode: e.meter, EventTime: timestamppb.New(e.at), IngestedAt: timestamppb.New(ingested), QuantityUnits: e.units,
			PeriodId: period.PeriodOf(e.at, f.loc), MeterVersion: 1, Properties: props,
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

func (f *fixture) readAgg(meter string, agg plansv1.Aggregation, ids ...string) []*aggregatorv1.UsageAggregate {
	f.t.Helper()
	ctx := tenant.NewContext(context.Background(), tenant.Info{ID: f.tenant})
	keys := make([]*aggregatorv1.UsageKey, len(ids))
	for i, id := range ids {
		keys[i] = &aggregatorv1.UsageKey{CustomerId: f.customer.String(), MeterCode: meter, PeriodId: id, Aggregation: agg}
	}
	b, err := f.queries.UsageBatch(ctx, keys, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return b.Usage
}

// monthRow is the stored quantity of the billing period row, "" when there is none.
func (f *fixture) monthRow(meter, periodID string) string {
	f.t.Helper()
	var q *string
	err := f.system.QueryRow(context.Background(), `SELECT quantity::text FROM usage_aggregates WHERE tenant_id = $1 AND meter_code = $2 AND period_id = $3 AND generation = 1`,
		f.tenant, meter, periodID).Scan(&q)
	if err != nil && err.Error() != "no rows in result set" {
		f.t.Fatal(err)
	}
	if q == nil {
		return ""
	}
	return *q
}

// lateRows are the late usage rows of a meter, "billed/origin" -> quantity.
func (f *fixture) lateRows(meter string) map[string]string {
	f.t.Helper()
	rows, err := f.system.Query(context.Background(), `SELECT billed_period_id, origin_period_id, quantity::text FROM late_usage WHERE tenant_id = $1 AND meter_code = $2 AND generation = 1`, f.tenant, meter)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var b, o, q string
		if err := rows.Scan(&b, &o, &q); err != nil {
			f.t.Fatal(err)
		}
		out[b+"/"+o] = q
	}
	return out
}

var allWindows = []period.Window{period.Hour, period.Day, period.Week, period.Month, period.Year, period.Lifetime}

func (f *fixture) windowIDs(at time.Time) []string {
	ids := make([]string, len(allWindows))
	for i, w := range allWindows {
		ids[i], _, _ = period.WindowOf(w, at, f.loc)
	}
	return ids
}

// distinctIn is the number of distinct users among the on-time events of a meter that fall in the window of kind w holding at; lastIn the quantity of the latest of them.
func (f *fixture) distinctIn(events []uev, meter string, w period.Window, at time.Time) string {
	target, _, _ := period.WindowOf(w, at, f.loc)
	users := map[string]bool{}
	for _, e := range events {
		if id, _, _ := period.WindowOf(w, e.at, f.loc); e.meter == meter && f.onTime(e) && id == target && e.user != "" {
			users[e.user] = true
		}
	}
	return fmt.Sprint(len(users))
}

func (f *fixture) lastIn(events []uev, meter string, w period.Window, at time.Time) (string, string) {
	target, _, _ := period.WindowOf(w, at, f.loc)
	var best *uev
	for i, e := range events {
		if id, _, _ := period.WindowOf(w, e.at, f.loc); e.meter != meter || !f.onTime(e) || id != target {
			continue
		}
		if best == nil || usage.OrderOf(usage.Event{EventTime: e.at, EventID: e.id}).After(usage.OrderOf(usage.Event{EventTime: best.at, EventID: best.id})) {
			best = &events[i]
		}
	}
	if best == nil {
		return "0", ""
	}
	return fmt.Sprint(best.units), best.id
}

// onTime is the classification of the fixture: ingested before the period's cutoff (its end plus the hour of grace the settings give).
func (f *fixture) onTime(e uev) bool {
	if e.ingested.IsZero() {
		return true
	}
	_, _, end := period.WindowOf(period.Month, e.at, f.loc)
	return e.ingested.Before(end.Add(time.Hour))
}

// A UNIQUE meter counts a value once however often, however many events and in whatever month, and every window kind answers from the sets: a day or a year is not the
// sum of its hours or months.
func TestUniqueCountsDistinctValuesInEveryWindow(t *testing.T) {
	for _, tz := range []string{"Europe/Istanbul", "America/New_York"} {
		t.Run(tz, func(t *testing.T) {
			f := newFixture(t, tz)
			now := f.now
			events := []uev{
				{meter: "unique_m", id: "e01", at: now.Add(-5 * time.Minute), user: "alice", units: 1},
				{meter: "unique_m", id: "e02", at: now.Add(-10 * time.Minute), user: "alice", units: 1},
				{meter: "unique_m", id: "e03", at: now.Add(-20 * time.Minute), user: "bob", units: 1},
				{meter: "unique_m", id: "e04", at: now.Add(-3 * time.Hour), user: "carol", units: 1},
				{meter: "unique_m", id: "e05", at: now.Add(-27 * time.Hour), user: "alice", units: 1},
				{meter: "unique_m", id: "e06", at: now.Add(-80 * time.Hour), user: "dave", units: 1},
				{meter: "unique_m", id: "e07", at: now.Add(-30 * 24 * time.Hour), user: "alice", units: 1},
				{meter: "unique_m", id: "e08", at: now.Add(-30 * 24 * time.Hour), user: "erin", units: 1},
				{meter: "unique_m", id: "e09", at: now.Add(-40 * 24 * time.Hour), user: "frank", units: 1},
				{meter: "unique_m", id: "e10", at: now.Add(-time.Hour), units: 1}, // no value: counts nothing
			}
			f.sendEvents(events[:4]...)
			f.sendEvents(events[4:]...)
			// The same event again is dropped by the inbox, and the same value in a new event adds nothing.
			f.sendEvents(events[0], uev{meter: "unique_m", id: "e11", at: now.Add(-6 * time.Minute), user: "bob", units: 1})
			events = append(events, uev{meter: "unique_m", id: "e11", at: now.Add(-6 * time.Minute), user: "bob", units: 1})

			got := f.readAgg("unique_m", plansv1.Aggregation_AGGREGATION_UNIQUE, f.windowIDs(now)...)
			for i, w := range allWindows {
				if want := f.distinctIn(events, "unique_m", w, now); got[i].GetQuantity() != want {
					t.Errorf("%s window %s = %s distinct, want %s", w, f.windowIDs(now)[i], got[i].GetQuantity(), want)
				}
			}
			if got[5].GetQuantity() == got[4].GetQuantity() && f.distinctIn(events, "unique_m", period.Lifetime, now) == "0" {
				t.Fatal("the fixture has no usage")
			}
			// The lifetime window has more values than any month: frank is only in an older month, and alice is in two months and counts once.
			if life, month := got[5].GetQuantity(), got[3].GetQuantity(); life <= month && f.distinctIn(events, "unique_m", period.Lifetime, now) != f.distinctIn(events, "unique_m", period.Month, now) {
				t.Errorf("lifetime %s should exceed the month %s", life, month)
			}
			// The stored count of the billing period is the number of values, and it is what billing rates.
			thisMonth := period.PeriodOf(now, f.loc)
			if row, want := f.monthRow("unique_m", thisMonth), f.distinctIn(events, "unique_m", period.Month, now); row != want {
				t.Errorf("usage_aggregates[%s] = %q, want %s", thisMonth, row, want)
			}
			if late := f.lateRows("unique_m"); len(late) != 0 {
				t.Errorf("late rows = %v, want none", late)
			}
			// A value is one set member however it is read; the month key answered from the row and the year key from the sets agree for a year of one month.
			if got[3].GetQuantityScale() != 0 {
				t.Errorf("a distinct count has scale %d, want 0", got[3].GetQuantityScale())
			}
		})
	}
}

// Late events of a UNIQUE meter: a value that was not counted on time is billed once as late usage in the first period a late event of it is billed in; one an on-time
// event carried is not billed again; and an on-time event arriving after the late one of the same value takes the unit back from late usage. The result is the same
// for every arrival order.
func TestUniqueLateValuesAndTheirConversion(t *testing.T) {
	f := newFixture(t, "UTC")
	// An old period of its own: the previous month's 10th.
	first := time.Date(f.now.Year(), f.now.Month(), 1, 0, 0, 0, 0, time.UTC)
	at := first.AddDate(0, -1, 9)
	origin := period.PeriodOf(at, f.loc)
	billed1 := period.PeriodOf(f.lateAt(at, 7*24*time.Hour), f.loc)
	billed2 := period.PeriodOf(f.lateAt(at, 45*24*time.Hour), f.loc)
	if origin >= billed1 || billed1 >= billed2 {
		t.Fatalf("the fixture's periods are not ordered: %s %s %s", origin, billed1, billed2)
	}
	late := func(id, user string, plus time.Duration) uev {
		return uev{meter: "unique_m", id: id, at: at, ingested: f.lateAt(at, plus), user: user, units: 1}
	}
	onTime := func(id, user string) uev {
		return uev{meter: "unique_m", id: id, at: at.Add(time.Hour), user: user, units: 1}
	}
	rows := func(want map[string]string, onTimeCount string) {
		t.Helper()
		got := f.lateRows("unique_m")
		for k, v := range got {
			if v == "0" {
				delete(got, k)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("late rows = %v, want %v", got, want)
		}
		if row := f.monthRow("unique_m", origin); row != onTimeCount {
			t.Errorf("usage_aggregates[%s] = %q, want %q", origin, row, onTimeCount)
		}
	}
	key := func(b string) string { return b + "/" + origin }

	f.sendEvents(late("l1", "x", 7*24*time.Hour))
	rows(map[string]string{key(billed1): "1"}, "")
	f.sendEvents(late("l2", "x", 7*24*time.Hour))
	rows(map[string]string{key(billed1): "1"}, "") // the same value again is the same unit
	f.sendEvents(late("l3", "y", 45*24*time.Hour))
	rows(map[string]string{key(billed1): "1", key(billed2): "1"}, "")
	f.sendEvents(late("l4", "y", 7*24*time.Hour)) // an earlier billing period for y: the unit moves
	rows(map[string]string{key(billed1): "2"}, "")
	f.sendEvents(onTime("o1", "x"))
	rows(map[string]string{key(billed1): "1"}, "1") // x was late, and now an on-time event carried it: the unit moves to the period itself
	f.sendEvents(onTime("o2", "z"), late("l5", "z", 7*24*time.Hour))
	rows(map[string]string{key(billed1): "1"}, "2") // z arrives on time and late in one batch: on time
	f.sendEvents(late("l6", "x", 7*24*time.Hour))
	rows(map[string]string{key(billed1): "1"}, "2") // already counted on time
}

// A UNIQUE meter's answer does not depend on the order or the batching of its events, and equals what the log says: the number of values with an on-time event per period,
// the number of values whose first late billing period is B per late row, and the distinct count of every window. A LAST meter's equals the quantity of the latest on-time
// event by (time, id), and late events of it change nothing.
func TestUniqueAndLastAreOrderIndependent(t *testing.T) {
	now := time.Now().UTC()
	r := rand.New(rand.NewPCG(7, 11))
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var events []uev
	for i := range 90 {
		meter := "unique_m"
		if i%2 == 1 {
			meter = "last_m"
		}
		// Few instants so that ties are common; two months so that a value repeats across periods.
		at := base.Add(-time.Duration(r.IntN(3)) * time.Hour).Add(-time.Duration(r.IntN(2)) * 30 * 24 * time.Hour).Add(-time.Duration(r.IntN(2)) * 24 * time.Hour)
		e := uev{meter: meter, id: fmt.Sprintf("ev%03d", r.IntN(1000)*100+i), at: at, units: int64(1 + r.IntN(50)), user: fmt.Sprintf("u%d", r.IntN(7))}
		if r.IntN(4) == 0 {
			e.ingested = time.Date(at.Year(), at.Month()+1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(7+r.IntN(2)*38) * 24 * time.Hour)
		}
		events = append(events, e)
	}

	type result struct {
		unique map[string]string
		last   map[string]string
		late   map[string]string
		months map[string]string
	}
	f := newFixture(t, "UTC")
	f.now = now
	run := func(seed uint64) result {
		// A new tenant and customer per run, in the same database.
		f.tenant, f.customer = uuid.New(), uuid.New()
		perm := rand.New(rand.NewPCG(seed, seed+1)).Perm(len(events))
		var batch []uev
		flush := func() {
			if len(batch) > 0 {
				f.sendEvents(batch...)
				batch = nil
			}
		}
		for n, i := range perm {
			batch = append(batch, events[i])
			if n%(3+int(seed%5)) == 0 {
				flush()
			}
		}
		flush()
		res := result{unique: map[string]string{}, last: map[string]string{}, months: map[string]string{}}
		ids := f.windowIDs(now)
		for i, u := range f.readAgg("unique_m", plansv1.Aggregation_AGGREGATION_UNIQUE, ids...) {
			res.unique[ids[i]] = u.GetQuantity()
		}
		for i, u := range f.readAgg("last_m", plansv1.Aggregation_AGGREGATION_LAST, ids...) {
			res.last[ids[i]] = u.GetQuantity() + "@" + u.GetLastEventId()
		}
		res.late = f.lateRows("unique_m")
		for k, v := range f.lateRows("last_m") {
			res.late["last:"+k] = v
		}
		for _, m := range []string{"unique_m", "last_m"} {
			for _, p := range []string{period.PeriodOf(now, f.loc), period.PeriodOf(now.AddDate(0, -1, 0), f.loc), period.PeriodOf(now.AddDate(0, -2, 0), f.loc)} {
				res.months[m+"/"+p] = f.monthRow(m, p)
			}
		}
		return res
	}
	first := run(1)
	for _, seed := range []uint64{2, 3, 4} {
		if got := run(seed); fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("seed %d changed the outcome:\n got %v\nwant %v", seed, got, first)
		}
	}

	// The outcome is the one the log implies.
	ids := f.windowIDs(now)
	for i, w := range allWindows {
		if want := f.distinctIn(events, "unique_m", w, now); first.unique[ids[i]] != want {
			t.Errorf("unique %s window %s = %s, want %s", w, ids[i], first.unique[ids[i]], want)
		}
		q, id := f.lastIn(events, "last_m", w, now)
		want := q + "@" + id
		if id == "" {
			want = "0@"
		}
		if first.last[ids[i]] != want {
			t.Errorf("last %s window %s = %s, want %s", w, ids[i], first.last[ids[i]], want)
		}
	}
	// Per period: the unique count of the row, and the late rows, from a model of the states of the values.
	type pv struct{ period, user string }
	state := map[pv]string{}
	seen := map[pv]bool{}
	for _, e := range events {
		if e.meter != "unique_m" {
			continue
		}
		k := pv{period.PeriodOf(e.at, f.loc), e.user}
		s := ""
		if !f.onTime(e) {
			s = period.PeriodOf(e.ingested, f.loc)
		}
		if !seen[k] || s < state[k] {
			state[k], seen[k] = s, true
		}
	}
	wantMonths, wantLate := map[string]int{}, map[string]int{}
	for k, s := range state {
		if s == "" {
			wantMonths[k.period]++
		} else {
			wantLate[s+"/"+k.period]++
		}
	}
	if len(wantMonths) == 0 || len(wantLate) == 0 {
		t.Fatalf("the fixture is too tame to prove anything: %d on-time rows, %d late rows", len(wantMonths), len(wantLate))
	}
	for p, n := range wantMonths {
		if got := first.months["unique_m/"+p]; got != fmt.Sprint(n) {
			t.Errorf("usage_aggregates unique_m %s = %q, want %d", p, got, n)
		}
	}
	gotLate := map[string]string{}
	for k, v := range first.late {
		if v != "0" && len(k) > 5 && k[:5] != "last:" {
			gotLate[k] = v
		}
	}
	var keys []string
	for k, n := range wantLate {
		keys = append(keys, k)
		if gotLate[k] != fmt.Sprint(n) {
			t.Errorf("late_usage unique_m %s = %q, want %d", k, gotLate[k], n)
		}
	}
	sort.Strings(keys)
	if len(gotLate) != len(wantLate) {
		t.Errorf("late rows %v, want exactly %v", gotLate, keys)
	}
	for k := range first.late {
		if len(k) > 5 && k[:5] == "last:" {
			t.Errorf("a LAST meter has late usage row %s: late events of it must not be stored", k)
		}
	}
}

// Late usage of a LAST meter is not stored: the last value of a closed period is final.
func TestLastIgnoresLateEvents(t *testing.T) {
	f := newFixture(t, "UTC")
	first := time.Date(f.now.Year(), f.now.Month(), 1, 0, 0, 0, 0, time.UTC)
	at := first.AddDate(0, -1, 9)
	origin := period.PeriodOf(at, f.loc)
	f.sendEvents(uev{meter: "last_m", id: "a", at: at, units: 4})
	f.sendEvents(uev{meter: "last_m", id: "b", at: at.Add(time.Hour), ingested: f.lateAt(at, 7*24*time.Hour), units: 99})
	if got := f.monthRow("last_m", origin); got != "4" {
		t.Errorf("last value of the closed period = %q, want 4: a late event must not change it", got)
	}
	if late := f.lateRows("last_m"); len(late) != 0 {
		t.Errorf("late rows = %v", late)
	}
	u := f.readAgg("last_m", plansv1.Aggregation_AGGREGATION_LAST, origin)[0]
	if u.GetQuantity() != "4" || u.GetLastEventId() != "a" || !u.GetLastEventTime().AsTime().Equal(at) {
		t.Errorf("read = %v @ %s %s", u.GetQuantity(), u.GetLastEventId(), u.GetLastEventTime().AsTime())
	}
	// An earlier event arriving after a later one does not take the value back; an equal time falls to the greater id.
	f.sendEvents(uev{meter: "last_m", id: "0", at: at.Add(-time.Hour), units: 1}, uev{meter: "last_m", id: "c", at: at, units: 8})
	if got := f.monthRow("last_m", origin); got != "8" {
		t.Errorf("after a tie on the time the greater id (c > a) wins: %q, want 8", got)
	}
}

// Both generations of a tenant that is being replayed get the sets and the counts (the live consumer writes the active generation and the catching-up one).
func TestUniqueIsWrittenToEveryTargetGeneration(t *testing.T) {
	f := newFixture(t, "UTC")
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO aggregate_generations (tenant_id, generation, status) VALUES ($1, 1, 'active')`,
		`INSERT INTO aggregate_generations (tenant_id, generation, status) VALUES ($1, 2, 'catching_up')`,
	} {
		if _, err := f.system.Exec(ctx, q, f.tenant); err != nil {
			t.Fatal(err)
		}
	}
	f.sendEvents(uev{meter: "unique_m", id: "a", at: f.now.Add(-time.Hour), user: "x", units: 1}, uev{meter: "unique_m", id: "b", at: f.now.Add(-time.Hour), user: "y", units: 1},
		uev{meter: "last_m", id: "c", at: f.now.Add(-time.Hour), units: 6})
	for g := 1; g <= 2; g++ {
		var users, hours, qty int
		if err := f.system.QueryRow(ctx, `SELECT count(*) FROM usage_distinct_values WHERE tenant_id = $1 AND generation = $2`, f.tenant, g).Scan(&users); err != nil {
			t.Fatal(err)
		}
		if err := f.system.QueryRow(ctx, `SELECT count(*) FROM usage_distinct_hours WHERE tenant_id = $1 AND generation = $2`, f.tenant, g).Scan(&hours); err != nil {
			t.Fatal(err)
		}
		if err := f.system.QueryRow(ctx, `SELECT quantity FROM usage_aggregates WHERE tenant_id = $1 AND generation = $2 AND meter_code = 'last_m'`, f.tenant, g).Scan(&qty); err != nil {
			t.Fatal(err)
		}
		if users != 2 || hours != 2 || qty != 6 {
			t.Errorf("generation %d: %d values, %d hour values, last %d; want 2, 2, 6", g, users, hours, qty)
		}
	}
}

// Two consumers writing the same values at once (different partitions of one customer, or the live and a replay consumer) never count a value twice or deadlock.
func TestUniqueConcurrentBatchesCountEachValueOnce(t *testing.T) {
	f := newFixture(t, "UTC")
	var users []string
	for i := range 40 {
		users = append(users, fmt.Sprintf("u%02d", i))
	}
	// The inbox partition of the month is created by the first batch that needs it; the race of two batches creating it is not what this test is about.
	if _, err := f.system.Exec(context.Background(), `SELECT ensure_processed_partition($1)`, period.PeriodOf(f.now.Add(-time.Hour), f.loc)); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 6)
	for w := range 6 {
		go func() {
			var recs []*kgo.Record
			rr := rand.New(rand.NewPCG(uint64(w), 5))
			for i, u := range rr.Perm(len(users)) {
				pb := &eventsv1.UsageEvent{
					EventId: fmt.Sprintf("w%d-%d", w, i), TenantId: f.tenant.String(), IdempotencyKey: fmt.Sprintf("k%d-%d", w, i), CustomerId: f.customer.String(), CustomerExternalId: "cus",
					MeterCode: "unique_m", EventTime: timestamppb.New(f.now.Add(-time.Hour)), IngestedAt: timestamppb.New(f.now.Add(-59 * time.Minute)),
					PeriodId: period.PeriodOf(f.now.Add(-time.Hour), f.loc), MeterVersion: 1, Properties: map[string]string{"user": users[u]},
				}
				b, _ := proto.Marshal(pb)
				recs = append(recs, &kgo.Record{Topic: "t", Partition: int32(w), Offset: int64(i), Value: b})
			}
			errs <- f.handler.Apply(context.Background(), int32(w), recs)
		}()
	}
	for range 6 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := f.monthRow("unique_m", period.PeriodOf(f.now.Add(-time.Hour), f.loc)); got != fmt.Sprint(len(users)) {
		t.Errorf("count = %q, want %d", got, len(users))
	}
	var n int
	if err := f.system.QueryRow(context.Background(), `SELECT count(*) FROM usage_distinct_values WHERE tenant_id = $1`, f.tenant).Scan(&n); err != nil || n != len(users) {
		t.Errorf("set members = %d, %v; want %d", n, err, len(users))
	}
}

// A replay carries the value sets of the periods it did not rebuild over to the new generation, clears what the target holds of the ones it did, and a discarded
// generation loses them, like the month and hour rows.
func TestGenerationCarryOverIncludesValueSets(t *testing.T) {
	f := newFixture(t, "UTC")
	f.sendEvents(uev{meter: "unique_m", id: "a", at: f.now.Add(-time.Hour), user: "x", units: 1})
	ctx := context.Background()
	q := sqlc.New(f.system)
	pid := period.PeriodOf(f.now.Add(-time.Hour), f.loc)
	count := func(table string, g int32) (n int) {
		if err := f.system.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1 AND generation = $2`, f.tenant, g).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	other := sqlc.CopyGenerationDistinctValuesOutsideParams{TenantID: f.tenant, Source: 1, Target: 2, FromPeriod: "2001-01", ToPeriod: "2001-01"}
	if err := q.CopyGenerationDistinctValuesOutside(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := q.CopyGenerationDistinctHoursOutside(ctx, sqlc.CopyGenerationDistinctHoursOutsideParams(other)); err != nil {
		t.Fatal(err)
	}
	if count("usage_distinct_values", 2) != 1 || count("usage_distinct_hours", 2) != 1 {
		t.Fatalf("the periods outside the replayed range were not carried: %d values, %d hour values", count("usage_distinct_values", 2), count("usage_distinct_hours", 2))
	}
	inside := sqlc.CopyGenerationDistinctValuesOutsideParams{TenantID: f.tenant, Source: 1, Target: 3, FromPeriod: pid, ToPeriod: pid}
	if err := q.CopyGenerationDistinctValuesOutside(ctx, inside); err != nil || count("usage_distinct_values", 3) != 0 {
		t.Fatalf("a replayed period was carried: %d, %v", count("usage_distinct_values", 3), err)
	}
	if err := q.ClearGenerationDistinctValuesOutside(ctx, sqlc.ClearGenerationDistinctValuesOutsideParams{TenantID: f.tenant, Generation: 2, FromPeriod: "2001-01", ToPeriod: "2001-01"}); err != nil || count("usage_distinct_values", 2) != 0 {
		t.Fatalf("clear outside the range: %d, %v", count("usage_distinct_values", 2), err)
	}
	if err := q.DeleteGenerationDistinctValues(ctx, sqlc.DeleteGenerationDistinctValuesParams{TenantID: f.tenant, Generation: 1}); err != nil || count("usage_distinct_values", 1) != 0 {
		t.Fatalf("delete generation: %d, %v", count("usage_distinct_values", 1), err)
	}
	if err := q.DeleteGenerationDistinctHours(ctx, sqlc.DeleteGenerationDistinctHoursParams{TenantID: f.tenant, Generation: 1}); err != nil || count("usage_distinct_hours", 1) != 0 {
		t.Fatalf("delete generation hours: %d, %v", count("usage_distinct_hours", 1), err)
	}
}

// The hour sets are a rolling window like the hour totals: maintenance deletes the old ones.
func TestMaintenancePrunesOldValueSetHours(t *testing.T) {
	f := newFixture(t, "UTC")
	f.sendEvents(uev{meter: "unique_m", id: "a", at: f.now.Add(-time.Hour), user: "x", units: 1}, uev{meter: "unique_m", id: "b", at: f.now.Add(-60 * 24 * time.Hour), user: "y", units: 1})
	count := func() (n int) {
		if err := f.system.QueryRow(context.Background(), `SELECT count(*) FROM usage_distinct_hours`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("hour sets after the batch = %d, want 1 (the old event has none)", count())
	}
	if _, err := f.system.Exec(context.Background(), `INSERT INTO usage_distinct_hours (tenant_id, customer_id, meter_code, generation, hour_id, value_hash) VALUES ($1, $2, 'unique_m', 1, '2001-01-01T00', sha256('z'::bytea))`, f.tenant, f.customer); err != nil {
		t.Fatal(err)
	}
	if n, err := maintenance.PruneWindows(context.Background(), f.system, time.Now()); err != nil || n != 1 || count() != 1 {
		t.Fatalf("PruneWindows = %d, %v; %d hour sets left", n, err, count())
	}
}
