//go:build integration

package itest

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	"github.com/0furkancolak/quotify/pkg/kafkax"
	"github.com/0furkancolak/quotify/pkg/outbox"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/ingest"
	"github.com/0furkancolak/quotify/services/ingest-svc/internal/maintenance"
)

// TestIngestSvc runs every scenario against one shared Postgres and Redpanda.
func TestIngestSvc(t *testing.T) {
	top = t
	t.Run("SameKeyConcurrentlyIsAcceptedOnce", testSameKeyConcurrently)
	t.Run("KeyReuseWithDifferentPayload", testKeyReuse)
	t.Run("MixedBatchKeepsOrderAndCodes", testMixedBatch)
	t.Run("SandboxWithoutKeyIsNotDeduplicated", testSandboxAutoKey)
	t.Run("KeysAreScopedPerTenant", testKeysPerTenant)
	t.Run("DedupAcrossMonthBoundary", testMonthBoundary)
	t.Run("DedupCoversMaxLateness", testDedupCoversMaxLateness)
	t.Run("PublishedRecordContentAndHeaders", testRecordContent)
	t.Run("WatermarkTracksOldestUnpublished", testWatermark)
	t.Run("BackPressure", testBackPressure)
	t.Run("PlansDownFailsTheBatchWithoutWriting", testLookupOutage)
	t.Run("RoleSeparationAndRLS", testRoleSeparation)
	t.Run("RelayCrashAfterProduceNeverLosesEvents", testRelayCrash)
	t.Run("KafkaPausedLosesNothing", testKafkaPaused)
	t.Run("PartitionMaintenance", testPartitionMaintenance) // last: drops old partitions
}

func status1(t *testing.T, r *ingestv1.EventResult, want ingestv1.EventResult_Status, code string) {
	t.Helper()
	if r.GetStatus() != want || r.GetErrorCode() != code {
		t.Fatalf("result = %s/%q (%s), want %s/%q", r.GetStatus(), r.GetErrorCode(), r.GetErrorMessage(), want, code)
	}
}

func testSameKeyConcurrently(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().Add(-time.Minute)

	const rounds = 10
	var accepted, duplicates int
	for i := 0; i < rounds; i++ {
		key := fmt.Sprintf("k-%d", i)
		results := make([]*ingestv1.EventResult, 3)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for j := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				resp, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{event(key, "1.5", at)}})
				if err != nil {
					t.Errorf("IngestEvents: %v", err)
					return
				}
				results[j] = resp.Results[0]
			}()
		}
		close(start)
		wg.Wait()
		if t.Failed() {
			return
		}
		a, d := 0, 0
		for _, r := range results {
			switch r.GetStatus() {
			case ingestv1.EventResult_STATUS_ACCEPTED:
				a++
			case ingestv1.EventResult_STATUS_DUPLICATE:
				d++
			default:
				t.Fatalf("round %d: unexpected %s %s", i, r.GetStatus(), r.GetErrorCode())
			}
			if r.GetEventId() != results[0].GetEventId() {
				t.Fatalf("round %d: event ids differ: %q vs %q", i, r.GetEventId(), results[0].GetEventId())
			}
		}
		if a != 1 || d != 2 {
			t.Fatalf("round %d: %d accepted, %d duplicates; want 1 and 2", i, a, d)
		}
		accepted += a
		duplicates += d
	}
	if got := outboxRows(t, tc.id); got != rounds {
		t.Fatalf("outbox rows = %d, want %d (one per key)", got, rounds)
	}

	stop := startRelay(t, outbox.Config{})
	defer stop()
	waitOutboxEmpty(t, tc.id, 20*time.Second)
	recs := consume(t, tc.id, rounds, 2*time.Second, 30*time.Second)
	if len(recs) != rounds {
		t.Fatalf("kafka records = %d, want exactly %d", len(recs), rounds)
	}
	t.Logf("%d accepted, %d duplicates, %d records", accepted, duplicates, len(recs))
}

func testKeyReuse(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().Add(-time.Hour)
	first := ingestOne(t, svc, tc, event("order-1", "10", at))
	status1(t, first, ingestv1.EventResult_STATUS_ACCEPTED, "")

	status1(t, ingestOne(t, svc, tc, event("order-1", "11", at)), ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REUSED")
	other := event("order-1", "10", at)
	other.Properties = map[string]string{"region": "eu"}
	status1(t, ingestOne(t, svc, tc, other), ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REUSED")
	// "10.000" is the same quantity as "10".
	same := ingestOne(t, svc, tc, event("order-1", "10.000", at))
	status1(t, same, ingestv1.EventResult_STATUS_DUPLICATE, "")
	if same.EventId != first.EventId {
		t.Fatalf("duplicate event id %q != original %q", same.EventId, first.EventId)
	}
	if got := outboxRows(t, tc.id); got != 1 {
		t.Fatalf("outbox rows = %d, want 1", got)
	}
}

func testMixedBatch(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().Add(-time.Hour)
	bad := func(mut func(*ingestv1.IncomingEvent)) *ingestv1.IncomingEvent {
		e := event(uuid.NewString(), "1", at)
		mut(e)
		return e
	}
	batch := []*ingestv1.IncomingEvent{
		event("b-1", "1.250", at),                                                                          // 0 accepted
		bad(func(e *ingestv1.IncomingEvent) { e.MeterCode = "nope" }),                                      // 1
		bad(func(e *ingestv1.IncomingEvent) { e.CustomerExternalId = "nobody" }),                           // 2
		bad(func(e *ingestv1.IncomingEvent) { e.Quantity = "-1" }),                                         // 3
		bad(func(e *ingestv1.IncomingEvent) { e.Quantity = "1.2345" }),                                     // 4
		bad(func(e *ingestv1.IncomingEvent) { e.IdempotencyKey = "" }),                                     // 5
		event("b-1", "1.25", at),                                                                           // 6 same key again in the batch: duplicate of #0
		event("b-1", "9", at),                                                                              // 7 same key, other payload
		bad(func(e *ingestv1.IncomingEvent) { e.EventTime = nil }),                                         // 8
		bad(func(e *ingestv1.IncomingEvent) { e.EventTime = timestamppbAt(at.Add(-40 * 24 * time.Hour)) }), // 9
		event("b-2", "2", at),                                                                              // 10 accepted
	}
	resp, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: batch})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		st   ingestv1.EventResult_Status
		code string
	}{
		{ingestv1.EventResult_STATUS_ACCEPTED, ""},
		{ingestv1.EventResult_STATUS_REJECTED, "UNKNOWN_METER"},
		{ingestv1.EventResult_STATUS_REJECTED, "UNKNOWN_CUSTOMER"},
		{ingestv1.EventResult_STATUS_REJECTED, "INVALID_QUANTITY"},
		{ingestv1.EventResult_STATUS_REJECTED, "INVALID_QUANTITY_PRECISION"},
		{ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REQUIRED"},
		{ingestv1.EventResult_STATUS_DUPLICATE, ""},
		{ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REUSED"},
		{ingestv1.EventResult_STATUS_REJECTED, "INVALID_EVENT_TIME"},
		{ingestv1.EventResult_STATUS_REJECTED, "EVENT_TOO_OLD"},
		{ingestv1.EventResult_STATUS_ACCEPTED, ""},
	}
	if len(resp.Results) != len(want) {
		t.Fatalf("%d results for %d events", len(resp.Results), len(want))
	}
	for i, w := range want {
		t.Run(fmt.Sprint(i), func(t *testing.T) { status1(t, resp.Results[i], w.st, w.code) })
	}
	if resp.Results[6].EventId != resp.Results[0].EventId {
		t.Fatal("in-batch duplicate should report the first event's id")
	}
	if got := outboxRows(t, tc.id); got != 2 {
		t.Fatalf("outbox rows = %d, want 2", got)
	}
}

func testSandboxAutoKey(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("sandbox")
	at := time.Now().Add(-time.Hour)
	a := ingestOne(t, svc, tc, event("", "1", at))
	b := ingestOne(t, svc, tc, event("", "1", at))
	status1(t, a, ingestv1.EventResult_STATUS_ACCEPTED, "")
	status1(t, b, ingestv1.EventResult_STATUS_ACCEPTED, "")
	if a.EventId == b.EventId {
		t.Fatal("sandbox events without a key must get distinct ids")
	}
	// Sandbox keys may still be supplied, and then dedup.
	status1(t, ingestOne(t, svc, tc, event("s-1", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	status1(t, ingestOne(t, svc, tc, event("s-1", "1", at)), ingestv1.EventResult_STATUS_DUPLICATE, "")
	if got := outboxRows(t, tc.id); got != 3 {
		t.Fatalf("outbox rows = %d, want 3", got)
	}
}

func testKeysPerTenant(t *testing.T) {
	svc := newService(t, testOpts{})
	a, b := newTenant("live"), newTenant("live")
	at := time.Now().Add(-time.Hour)
	ra := ingestOne(t, svc, a, event("shared-key", "1", at))
	rb := ingestOne(t, svc, b, event("shared-key", "1", at))
	status1(t, ra, ingestv1.EventResult_STATUS_ACCEPTED, "")
	status1(t, rb, ingestv1.EventResult_STATUS_ACCEPTED, "")
	if ra.EventId == rb.EventId {
		t.Fatal("tenants must not share dedup state")
	}
}

// An event may be sent up to 35 days after its event_time, so a repeat of a key first seen at the end of January has to be
// recognised until early March: two months after the bucket it was recorded in.
func testDedupCoversMaxLateness(t *testing.T) {
	clk := newClock(time.Date(2026, 1, 31, 23, 59, 0, 0, time.UTC))
	svc := newService(t, testOpts{clock: clk})
	if _, _, err := maintenance.Once(context.Background(), shared.system, clk.Now()); err != nil {
		t.Fatal(err)
	}
	tc := newTenant("live")
	at := time.Date(2026, 1, 30, 12, 0, 0, 0, time.UTC)
	first := ingestOne(t, svc, tc, event("late-repeat", "7", at))
	status1(t, first, ingestv1.EventResult_STATUS_ACCEPTED, "")

	clk.Set(time.Date(2026, 3, 1, 0, 1, 0, 0, time.UTC)) // the event is 30 days old: still acceptable, so still deduplicated
	again := ingestOne(t, svc, tc, event("late-repeat", "7", at))
	status1(t, again, ingestv1.EventResult_STATUS_DUPLICATE, "")
	if again.EventId != first.EventId {
		t.Fatalf("duplicate id %q != %q", again.EventId, first.EventId)
	}
	if got := outboxRows(t, tc.id); got != 1 {
		t.Fatalf("outbox rows = %d, want 1", got)
	}
}

func testMonthBoundary(t *testing.T) {
	clk := newClock(time.Date(2026, 11, 30, 23, 59, 30, 0, time.UTC))
	svc := newService(t, testOpts{clock: clk})
	if _, _, err := maintenance.Once(context.Background(), shared.system, clk.Now()); err != nil {
		t.Fatal(err)
	}
	tc := newTenant("live")
	at := clk.Now().Add(-time.Minute)

	first := ingestOne(t, svc, tc, event("boundary", "7", at))
	status1(t, first, ingestv1.EventResult_STATUS_ACCEPTED, "")

	clk.Set(time.Date(2026, 12, 1, 0, 0, 10, 0, time.UTC)) // next month's partition now
	retry := ingestOne(t, svc, tc, event("boundary", "7", at))
	status1(t, retry, ingestv1.EventResult_STATUS_DUPLICATE, "")
	if retry.EventId != first.EventId {
		t.Fatalf("cross-partition duplicate id %q != %q", retry.EventId, first.EventId)
	}
	status1(t, ingestOne(t, svc, tc, event("boundary", "8", at)), ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REUSED")
	if got := outboxRows(t, tc.id); got != 1 {
		t.Fatalf("outbox rows = %d, want 1", got)
	}

	// The key now also lives in the December bucket only if it was accepted there: it wasn't, so a new key does.
	status1(t, ingestOne(t, svc, tc, event("december", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	var buckets []time.Time
	err := pgxx.InTenantTx(context.Background(), shared.app, tc.id, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT created_at FROM idempotency_keys ORDER BY idempotency_key`)
		if err != nil {
			return err
		}
		buckets, err = pgx.CollectRows(rows, pgx.RowTo[time.Time])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	wantBuckets := []time.Time{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	if len(buckets) != 2 || !buckets[0].Equal(wantBuckets[0]) || !buckets[1].Equal(wantBuckets[1]) {
		t.Fatalf("buckets = %v, want %v", buckets, wantBuckets)
	}
}

func testRecordContent(t *testing.T) {
	// A fixed trace context in the request must come out as Kafka headers.
	tp := propagation.TraceContext{}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:  trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8}, TraceFlags: trace.FlagsSampled,
	})
	svc := newService(t, testOpts{propagator: tp})
	tc := newTenant("live")
	ctx := trace.ContextWithSpanContext(tc.ctx, sc)
	at := time.Now().UTC().Add(-time.Hour)
	ev := event("content-1", "12.345", at)
	// 22:00Z on the last UTC day of the previous month is 01:00 on the 1st in Istanbul (UTC+3): the current period.
	// It is at most 31 days old, so it is inside the 35-day window whatever day the test runs.
	ev.EventTime = timestamppbAt(time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC).Add(-2 * time.Hour))
	ev.Properties = map[string]string{"region": "eu", "model": "x"}
	resp, err := svc.IngestEvents(ctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{ev}})
	if err != nil {
		t.Fatal(err)
	}
	status1(t, resp.Results[0], ingestv1.EventResult_STATUS_ACCEPTED, "")

	stop := startRelay(t, outbox.Config{})
	defer stop()
	recs := consume(t, tc.id, 1, time.Second, 30*time.Second)
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	r := recs[0]
	h := kafkax.HeaderMap(r.Headers)
	wantTP := "00-" + sc.TraceID().String() + "-" + sc.SpanID().String() + "-01"
	if h["traceparent"] != wantTP || h["ce-type"] != "quotify.events.v1.UsageEvent" || h["tenant-id"] != tc.id.String() ||
		h["content-type"] != "application/x-protobuf" || h["ce-id"] != resp.Results[0].EventId {
		t.Fatalf("headers = %v (want traceparent %s)", h, wantTP)
	}
	e := decode(t, r)
	if e.EventId != resp.Results[0].EventId || e.TenantId != tc.id.String() || e.MeterCode != "gb_hours" || e.MeterVersion != 2 ||
		e.QuantityUnits != 12345 || e.QuantityScale != 3 || e.CustomerExternalId != "cus_1" || e.IdempotencyKey != "content-1" ||
		e.Properties["region"] != "eu" || e.IngestedAt == nil || !e.EventTime.AsTime().Equal(ev.EventTime.AsTime()) {
		t.Fatalf("event = %v", e)
	}
	cur := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	if want := fmt.Sprintf("%04d-%02d", cur.Year(), int(cur.Month())); e.PeriodId != want {
		t.Fatalf("period_id = %s, want %s", e.PeriodId, want)
	}
}

func testWatermark(t *testing.T) {
	svc := newService(t, testOpts{})
	ctx := context.Background()
	w, err := svc.GetOutboxWatermark(ctx, &ingestv1.GetOutboxWatermarkRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if w.OldestUnpublishedIngestedAt != nil || w.ServerNow == nil {
		t.Fatalf("empty outbox watermark = %v", w)
	}
	tc := newTenant("live")
	at := time.Now().Add(-time.Hour)
	before := time.Now().Add(-time.Second)
	ingestOne(t, svc, tc, event("w-1", "1", at))
	time.Sleep(20 * time.Millisecond)
	ingestOne(t, svc, tc, event("w-2", "1", at))
	w, err = svc.GetOutboxWatermark(ctx, &ingestv1.GetOutboxWatermarkRequest{})
	if err != nil {
		t.Fatal(err)
	}
	oldest := w.OldestUnpublishedIngestedAt.AsTime()
	if oldest.Before(before) || oldest.After(time.Now()) {
		t.Fatalf("watermark %s not within the first ingest", oldest)
	}
	var minDB time.Time
	if err := shared.system.QueryRow(ctx, `SELECT min(ingested_at) FROM outbox`).Scan(&minDB); err != nil {
		t.Fatal(err)
	}
	if !oldest.Equal(minDB) {
		t.Fatalf("watermark %s != min(ingested_at) %s", oldest, minDB)
	}

	stop := startRelay(t, outbox.Config{})
	defer stop()
	waitOutboxEmpty(t, tc.id, 20*time.Second)
	w, err = svc.GetOutboxWatermark(ctx, &ingestv1.GetOutboxWatermarkRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if w.OldestUnpublishedIngestedAt != nil {
		t.Fatalf("drained outbox still has watermark %s", w.OldestUnpublishedIngestedAt.AsTime())
	}
}

func testBackPressure(t *testing.T) {
	svc := newService(t, testOpts{maxOutboxDepth: 5})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = svc.Run(ctx) }()
	tc := newTenant("live")
	at := time.Now().Add(-time.Hour)
	var batch []*ingestv1.IncomingEvent
	for i := 0; i < 10; i++ {
		batch = append(batch, event(fmt.Sprintf("bp-%d", i), "1", at))
	}
	if _, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: batch}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for svc.OutboxDepth() <= 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	_, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: batch[:1]})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("err = %v, want ResourceExhausted", err)
	}
	// After the relay drains the table, ingest resumes.
	stop := startRelay(t, outbox.Config{})
	defer stop()
	waitOutboxEmpty(t, tc.id, 20*time.Second)
	deadline = time.Now().Add(5 * time.Second)
	for svc.OutboxDepth() > 5 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	status1(t, ingestOne(t, svc, tc, event("bp-after", "1", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
}

func testLookupOutage(t *testing.T) {
	svc := newService(t, testOpts{lookups: fakeLookups{err: status.Error(codes.Unavailable, "plans down")}})
	tc := newTenant("live")
	_, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{event("x", "1", time.Now().Add(-time.Hour))}})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
	if got := outboxRows(t, tc.id); got != 0 {
		t.Fatalf("outbox rows = %d after a failed batch", got)
	}
}

func testRoleSeparation(t *testing.T) {
	svc := newService(t, testOpts{})
	ctx := context.Background()
	a, b := newTenant("live"), newTenant("live")
	at := time.Now().Add(-time.Hour)
	ingestOne(t, svc, a, event("rls-a", "1", at))
	ingestOne(t, svc, b, event("rls-b", "1", at))

	if err := pgxx.VerifyForcedRLS(ctx, shared.system, []string{"outbox"}); err != nil {
		t.Fatalf("forced RLS: %v", err)
	}
	tables, err := pgxx.TenantTables(ctx, shared.system, []string{"outbox"})
	if err != nil || len(tables) != 1 || tables[0] != "idempotency_keys" {
		t.Fatalf("tenant tables = %v, %v", tables, err)
	}

	// Tenant A sees only its own keys, even when it asks for B's.
	err = pgxx.InTenantTx(ctx, shared.app, a.id, func(tx pgx.Tx) error {
		var own, others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys`).Scan(&own); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE tenant_id = $1`, b.id).Scan(&others); err != nil {
			return err
		}
		if own != 1 || others != 0 {
			return fmt.Errorf("tenant A sees %d own and %d foreign keys", own, others)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Forging a row for another tenant is blocked by the WITH CHECK policy.
	err = pgxx.InTenantTx(ctx, shared.app, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO idempotency_keys VALUES ($1, 'forged', 'e', '\x00', now())`, b.id)
		return err
	})
	if pgxx.ErrCode(err) != "42501" {
		t.Fatalf("forged insert: %v", err)
	}

	// The app role can't read or write the outbox directly, and can't enqueue for another tenant.
	if _, err := shared.app.Exec(ctx, `SELECT count(*) FROM outbox`); pgxx.ErrCode(err) != "42501" {
		t.Fatalf("app read of outbox: %v", err)
	}
	if _, err := shared.app.Exec(ctx, `DELETE FROM idempotency_keys`); pgxx.ErrCode(err) != "42501" {
		t.Fatalf("app delete: %v", err)
	}
	err = pgxx.InTenantTx(ctx, shared.app, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT enqueue_outbox($1, 'usage.events.v1', 'k', ARRAY['\x00'::bytea], ARRAY['{}'], now())`, b.id)
		return err
	})
	if pgxx.ErrCode(err) != "42501" {
		t.Fatalf("enqueue for another tenant: %v", err)
	}
	// The migrator-only maintenance function isn't callable by the app role.
	if _, err := shared.app.Exec(ctx, `SELECT maintain_idempotency_partitions(now())`); pgxx.ErrCode(err) != "42501" {
		t.Fatalf("app call of maintenance: %v", err)
	}
}

func testRelayCrash(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().Add(-time.Hour)
	const total = 2000
	accepted := map[string]bool{}
	for i := 0; i < total; i += 500 {
		var batch []*ingestv1.IncomingEvent
		for j := i; j < i+500; j++ {
			batch = append(batch, event(fmt.Sprintf("crash-%d", j), "1", at))
		}
		resp, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: batch})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range resp.Results {
			accepted[r.EventId] = true
		}
	}

	// RELAY_CRASH_AFTER_PRODUCE=0.2: after the broker acked, 20% of batches "crash" before the delete commits.
	var n int
	var mu sync.Mutex
	stop := startRelay(t, outbox.Config{BatchSize: 100, Workers: 3, ErrorBackoff: 10 * time.Millisecond, AfterPublish: func() error {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n%5 == 0 { // deterministic 20%
			return fmt.Errorf("simulated crash")
		}
		return nil
	}})
	defer stop()
	waitOutboxEmpty(t, tc.id, 60*time.Second)

	recs := consume(t, tc.id, total, 3*time.Second, 60*time.Second)
	seen := map[string][]byte{}
	for _, r := range recs {
		e := decode(t, r)
		if prev, ok := seen[e.EventId]; ok && string(prev) != string(r.Value) {
			t.Fatalf("event %s republished with different bytes", e.EventId)
		}
		seen[e.EventId] = r.Value
	}
	for id := range accepted {
		if _, ok := seen[id]; !ok {
			t.Fatalf("accepted event %s never reached Kafka", id)
		}
	}
	if len(seen) != total {
		t.Fatalf("distinct events in Kafka = %d, want %d", len(seen), total)
	}
	if len(recs) <= total {
		t.Fatalf("records = %d: expected duplicates from the simulated crashes", len(recs))
	}
	t.Logf("%d records for %d events (%d republished)", len(recs), total, len(recs)-total)
}

func testKafkaPaused(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().Add(-time.Hour)
	pause := 20 * time.Second
	if v := os.Getenv("ITEST_KAFKA_PAUSE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		pause = d
	}

	stop := startRelay(t, outbox.Config{ErrorBackoff: 200 * time.Millisecond})
	defer stop()
	dockerPause(t, true)
	paused := true
	defer func() {
		if paused {
			dockerPause(t, false)
		}
	}()

	const total = 10000
	accepted := map[string]bool{}
	started := time.Now()
	for i := 0; i < total; i += 500 {
		var batch []*ingestv1.IncomingEvent
		for j := i; j < i+500; j++ {
			batch = append(batch, event(fmt.Sprintf("pause-%d", j), "1", at))
		}
		resp, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: batch})
		if err != nil {
			t.Fatalf("ingest while Kafka is paused must keep working: %v", err)
		}
		for _, r := range resp.Results {
			if r.Status != ingestv1.EventResult_STATUS_ACCEPTED {
				t.Fatalf("result %s %s", r.Status, r.ErrorCode)
			}
			accepted[r.EventId] = true
		}
	}
	if len(accepted) != total {
		t.Fatalf("accepted %d, want %d", len(accepted), total)
	}
	if left := pause - time.Since(started); left > 0 {
		time.Sleep(left)
	}
	if got := outboxRows(t, tc.id); got == 0 {
		t.Fatal("outbox drained while Kafka was paused")
	}
	dockerPause(t, false)
	paused = false

	waitOutboxEmpty(t, tc.id, 2*time.Minute)
	recs := consume(t, tc.id, total, 3*time.Second, 2*time.Minute)
	seen := map[string]bool{}
	for _, r := range recs {
		seen[decode(t, r).EventId] = true
	}
	for id := range accepted {
		if !seen[id] {
			t.Fatalf("accepted event %s missing from Kafka", id)
		}
	}
	if len(seen) != total {
		t.Fatalf("distinct events in Kafka = %d, want %d", len(seen), total)
	}
	t.Logf("paused %s; %d records for %d events", pause, len(recs), total)
}

func testPartitionMaintenance(t *testing.T) {
	infra(t)
	ctx := context.Background()
	now := time.Date(2030, 3, 15, 12, 0, 0, 0, time.UTC)
	created, dropped, err := maintenance.Once(ctx, shared.system, now)
	if err != nil {
		t.Fatal(err)
	}
	wantCreated := []string{"idempotency_keys_y2030m03", "idempotency_keys_y2030m04", "idempotency_keys_y2030m05"}
	if fmt.Sprint(created) != fmt.Sprint(wantCreated) {
		t.Fatalf("created = %v, want %v", created, wantCreated)
	}
	if len(dropped) == 0 {
		t.Fatal("old partitions were not dropped")
	}
	// Idempotent: a second run does nothing.
	created, dropped, err = maintenance.Once(ctx, shared.system, now)
	if err != nil || len(created) != 0 || len(dropped) != 0 {
		t.Fatalf("second run: %v %v %v", created, dropped, err)
	}
	// The partition whose month ended 44 days ago is kept (2030-01 ended Feb 1; now-45d = Jan 29 -> Jan stays, Dec goes).
	var names []string
	rows, err := shared.system.Query(ctx, `SELECT c.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid WHERE i.inhparent = 'idempotency_keys'::regclass ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	if names, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n < "idempotency_keys_y2030m01" {
			t.Fatalf("partition %s should have been dropped (all: %v)", n, names)
		}
	}
}

var _ = ingest.Topic
