//go:build integration

package itest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/grpcapi"
)

// correct is a correction of the event keyed corrects: the quantity it should have had, and why.
func correct(key, corrects, qty string) *ingestv1.IncomingEvent {
	return &ingestv1.IncomingEvent{IdempotencyKey: key, Corrects: corrects, Reason: "entered in the wrong unit", CustomerExternalId: "cus_1", MeterCode: "gb_hours", Quantity: qty}
}

func ingestAs(t *testing.T, svc *grpcapi.Server, tc tenantCtx, actor string, evs ...*ingestv1.IncomingEvent) []*ingestv1.EventResult {
	t.Helper()
	resp, err := svc.IngestEvents(tc.ctx, &ingestv1.IngestEventsRequest{Events: evs, Actor: actor})
	if err != nil {
		t.Fatalf("IngestEvents: %v", err)
	}
	return resp.Results
}

// queryTenant runs fn over the tenant's own rows, wherever they are (the pooled database, or the tenant's schema in a cell).
func queryTenant(t *testing.T, tenantID uuid.UUID, fn func(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) error) {
	t.Helper()
	ctx := context.Background()
	if shared.router != nil {
		if err := pgxx.InTenantSchemaTx(ctx, shared.cellSys, tenantID, func(tx pgx.Tx) error { return fn(ctx, tx) }); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := fn(ctx, shared.system); err != nil {
		t.Fatal(err)
	}
}

// outboxEvents are the events waiting for the relay, oldest first.
func outboxEvents(t *testing.T, tenantID uuid.UUID) []*eventsv1.UsageEvent {
	t.Helper()
	var out []*eventsv1.UsageEvent
	queryTenant(t, tenantID, func(ctx context.Context, q interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	}) error {
		rows, err := q.Query(ctx, `SELECT payload FROM outbox WHERE tenant_id = $1 ORDER BY id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				return err
			}
			e := &eventsv1.UsageEvent{}
			if err := proto.Unmarshal(b, e); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out
}

type correctionRow struct {
	key, corrects, reason, by string
	delta                     int64
}

func correctionRows(t *testing.T, tenantID uuid.UUID) []correctionRow {
	t.Helper()
	var out []correctionRow
	queryTenant(t, tenantID, func(ctx context.Context, q interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	}) error {
		rows, err := q.Query(ctx, `SELECT correction_key, corrects_key, reason, corrected_by, delta_units FROM event_corrections WHERE tenant_id = $1 ORDER BY created_at, event_id`, tenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r correctionRow
			if err := rows.Scan(&r.key, &r.corrects, &r.reason, &r.by, &r.delta); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out
}

// A correction names the event, says what its quantity should have been, and the service publishes the difference as a signed event in the original's period: once, whatever the
// retries, and a second correction of the same event is measured against the first.
func testCorrectionReplacesTheQuantity(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().UTC().Add(-time.Hour)
	orig := ingestOne(t, svc, tc, event("ord-1", "10", at))
	status1(t, orig, ingestv1.EventResult_STATUS_ACCEPTED, "")

	// 10 becomes 8: minus 2 (2000 units at scale 3), in the original's time and period.
	first := ingestAs(t, svc, tc, "ada@example.com", correct("fix-1", "ord-1", "8"))[0]
	status1(t, first, ingestv1.EventResult_STATUS_ACCEPTED, "")
	events := outboxEvents(t, tc.id)
	if len(events) != 2 {
		t.Fatalf("outbox has %d events, want the original and the correction", len(events))
	}
	c := events[1]
	if c.EventId != first.EventId || c.QuantityUnits != -2000 || c.QuantityScale != 3 || c.MeterCode != "gb_hours" || c.CustomerExternalId != "cus_1" ||
		!c.EventTime.AsTime().Equal(events[0].EventTime.AsTime().Truncate(time.Microsecond)) || c.PeriodId != events[0].PeriodId || c.IdempotencyKey != "fix-1" {
		t.Fatalf("correction event = %v", c)
	}
	if c.Correction == nil || c.Correction.CorrectsEventId != orig.EventId || c.Correction.Reason != "entered in the wrong unit" || c.Correction.CorrectedBy != "ada@example.com" || len(c.Properties) != 0 {
		t.Fatalf("correction marker = %v", c.Correction)
	}

	// The same correction again is a duplicate: nothing more is published or recorded.
	again := ingestAs(t, svc, tc, "ada@example.com", correct("fix-1", "ord-1", "8"))[0]
	status1(t, again, ingestv1.EventResult_STATUS_DUPLICATE, "")
	if again.EventId != first.EventId {
		t.Fatalf("duplicate event id %q != %q", again.EventId, first.EventId)
	}
	// The key with another quantity is a reuse, not a second correction.
	status1(t, ingestAs(t, svc, tc, "ada@example.com", correct("fix-1", "ord-1", "9"))[0], ingestv1.EventResult_STATUS_REJECTED, "IDEMPOTENCY_KEY_REUSED")
	if n := len(outboxEvents(t, tc.id)); n != 2 {
		t.Fatalf("outbox has %d events after the retries, want 2", n)
	}

	// A second correction is measured against the first: 8 becomes 12, plus 4; then it is cancelled, minus 12. The original's own 10 plus these is 0.
	status1(t, ingestAs(t, svc, tc, "key:k1", correct("fix-2", "ord-1", "12"))[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	status1(t, ingestAs(t, svc, tc, "key:k1", correct("fix-3", "ord-1", "0"))[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	// One that changes nothing is accepted and moves nothing.
	status1(t, ingestAs(t, svc, tc, "key:k1", correct("fix-4", "ord-1", "0"))[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	events = outboxEvents(t, tc.id)
	var deltas []int64
	sum := events[0].QuantityUnits
	for _, e := range events[1:] {
		deltas = append(deltas, e.QuantityUnits)
		sum += e.QuantityUnits
	}
	if fmt.Sprint(deltas) != "[-2000 4000 -12000 0]" || sum != 0 {
		t.Fatalf("differences = %v, effective quantity %d; want [-2000 4000 -12000 0] and 0", deltas, sum)
	}

	// The record: who, why, what, in order.
	rows := correctionRows(t, tc.id)
	if len(rows) != 4 || rows[0] != (correctionRow{"fix-1", "ord-1", "entered in the wrong unit", "ada@example.com", -2000}) || rows[2].delta != -12000 || rows[2].by != "key:k1" {
		t.Fatalf("event_corrections = %+v", rows)
	}
}

// Every refusal says why, and none of them claims the key: the same key can be used for the fixed request.
func testCorrectionRefusals(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().UTC().Add(-time.Hour)
	status1(t, ingestOne(t, svc, tc, event("ord-1", "10", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")
	peak := event("peak-1", "7", at)
	peak.MeterCode = "peak_seats"
	status1(t, ingestOne(t, svc, tc, peak), ingestv1.EventResult_STATUS_ACCEPTED, "")
	eu := event("eu-1", "7", at)
	eu.MeterCode = "eu_calls"
	status1(t, ingestOne(t, svc, tc, eu), ingestv1.EventResult_STATUS_ACCEPTED, "")
	status1(t, ingestAs(t, svc, tc, "k", correct("fix-ok", "ord-1", "9"))[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	published := len(outboxEvents(t, tc.id))

	with := func(key string, mut func(*ingestv1.IncomingEvent)) *ingestv1.IncomingEvent {
		e := correct(key, "ord-1", "8")
		mut(e)
		return e
	}
	for _, c := range []struct {
		name string
		ev   *ingestv1.IncomingEvent
		code string
	}{
		{"an event nobody sent", correct("bad-1", "ord-404", "1"), "CORRECTION_UNKNOWN_EVENT"},
		{"another customer", with("bad-2", func(e *ingestv1.IncomingEvent) { e.CustomerExternalId = "cus_2" }), "CORRECTION_MISMATCH"},
		{"another meter", with("bad-3", func(e *ingestv1.IncomingEvent) { e.MeterCode = "api_calls" }), "CORRECTION_MISMATCH"},
		{"a time of its own", with("bad-4", func(e *ingestv1.IncomingEvent) { e.EventTime = timestamppb.New(at) }), "INVALID_CORRECTION"},
		{"a correction of a correction", correct("bad-5", "fix-ok", "1"), "CORRECTION_OF_CORRECTION"},
		{"a maximum", &ingestv1.IncomingEvent{IdempotencyKey: "bad-6", Corrects: "peak-1", Reason: "r", CustomerExternalId: "cus_1", MeterCode: "peak_seats", Quantity: "5"}, "CORRECTION_UNSUPPORTED_METER"},
		{"a filtered meter", &ingestv1.IncomingEvent{IdempotencyKey: "bad-7", Corrects: "eu-1", Reason: "r", CustomerExternalId: "cus_1", MeterCode: "eu_calls", Quantity: "5"}, "CORRECTION_UNSUPPORTED_METER"},
		{"no reason", with("bad-8", func(e *ingestv1.IncomingEvent) { e.Reason = "  " }), "INVALID_CORRECTION"},
		{"a reason with nothing to correct", with("bad-9", func(e *ingestv1.IncomingEvent) { e.Corrects = "" }), "INVALID_CORRECTION"},
		{"properties", with("bad-10", func(e *ingestv1.IncomingEvent) { e.Properties = map[string]string{"region": "eu"} }), "INVALID_CORRECTION"},
		{"itself", with("bad-11", func(e *ingestv1.IncomingEvent) { e.Corrects = "bad-11" }), "INVALID_CORRECTION"},
		{"a reason that is too long", with("bad-12", func(e *ingestv1.IncomingEvent) { e.Reason = string(make([]byte, 501)) + "x" }), "INVALID_CORRECTION"},
		{"no key", with("", func(*ingestv1.IncomingEvent) {}), "IDEMPOTENCY_KEY_REQUIRED"},
		{"a negative quantity", with("bad-13", func(e *ingestv1.IncomingEvent) { e.Quantity = "-1" }), "INVALID_QUANTITY"},
		{"too many decimals", with("bad-14", func(e *ingestv1.IncomingEvent) { e.Quantity = "1.2345" }), "INVALID_QUANTITY_PRECISION"},
	} {
		got := ingestAs(t, svc, tc, "k", c.ev)[0]
		if got.GetStatus() != ingestv1.EventResult_STATUS_REJECTED || got.GetErrorCode() != c.code {
			t.Errorf("%s: %s/%q (%s), want REJECTED/%q", c.name, got.GetStatus(), got.GetErrorCode(), got.GetErrorMessage(), c.code)
		}
	}
	if n := len(outboxEvents(t, tc.id)); n != published {
		t.Fatalf("refusals published %d events", n-published)
	}
	// A refused request claimed no key: the same key does its job when the request is right.
	status1(t, ingestAs(t, svc, tc, "k", correct("bad-1", "ord-1", "7"))[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	// Another tenant cannot correct this tenant's event, whatever it knows of its key.
	other := newTenant("live")
	status1(t, ingestAs(t, svc, other, "k", correct("fix-x", "ord-1", "1"))[0], ingestv1.EventResult_STATUS_REJECTED, "CORRECTION_UNKNOWN_EVENT")
}

// Corrections of one event made at the same time each see the other's: whatever order the lock grants them, the differences add up to the quantity of the one that went last,
// and both are accepted.
func testConcurrentCorrections(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().UTC().Add(-time.Hour)
	status1(t, ingestOne(t, svc, tc, event("ord-1", "10", at)), ingestv1.EventResult_STATUS_ACCEPTED, "")

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := ingestAs(t, svc, tc, "k", correct(fmt.Sprintf("fix-%d", i), "ord-1", fmt.Sprint(i+1)))[0]
			if r.GetStatus() != ingestv1.EventResult_STATUS_ACCEPTED {
				t.Errorf("writer %d: %s %s", i, r.GetStatus(), r.GetErrorCode())
			}
		}()
	}
	close(start)
	wg.Wait()
	events := outboxEvents(t, tc.id)
	if len(events) != writers+1 {
		t.Fatalf("outbox has %d events, want %d", len(events), writers+1)
	}
	// Row order is commit order; each difference was measured against everything committed before it, so the running total after any prefix is a quantity somebody asked for.
	asked := map[int64]bool{}
	for i := range writers {
		asked[int64(i+1)*1000] = true
	}
	total := events[0].QuantityUnits
	for _, e := range events[1:] {
		total += e.QuantityUnits
		if !asked[total] {
			t.Fatalf("after %s the event stands at %d, which nobody asked for: the corrections did not see each other", e.IdempotencyKey, total)
		}
	}
}

// A COUNT event is one whatever its quantity, so its corrections are cancelling it and counting it again.
func testCountCorrections(t *testing.T) {
	svc := newService(t, testOpts{})
	tc := newTenant("live")
	at := time.Now().UTC().Add(-time.Hour)
	counted := event("req-1", "5", at)
	counted.MeterCode = "requests"
	status1(t, ingestOne(t, svc, tc, counted), ingestv1.EventResult_STATUS_ACCEPTED, "")
	fix := func(key, qty string) {
		t.Helper()
		e := correct(key, "req-1", qty)
		e.MeterCode = "requests"
		status1(t, ingestAs(t, svc, tc, "k", e)[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	}
	fix("c-1", "0") // cancelled: -1
	fix("c-2", "0") // already cancelled: nothing
	fix("c-3", "3") // counted again: +1, not +3
	fix("c-4", "5") // already counted: nothing
	var deltas []int64
	for _, e := range outboxEvents(t, tc.id)[1:] {
		deltas = append(deltas, e.QuantityUnits)
	}
	if fmt.Sprint(deltas) != "[-1 0 1 0]" {
		t.Fatalf("differences = %v, want [-1 0 1 0]", deltas)
	}
}

// A correction is possible while the key of the original is remembered, which the lookup reaches back over the current and the two previous months for: within it the
// correction is accepted, beyond it the original is unknown.
func testCorrectionWindow(t *testing.T) {
	if cellsMode() {
		t.Skip("the key lookup is the same code over the tenant's table; the clock is what this test moves")
	}
	infra(t)
	clk := newClock(time.Date(2031, 3, 10, 12, 0, 0, 0, time.UTC))
	svc := newService(t, testOpts{clock: clk})
	tc := newTenant("live")
	// Partitions for the months the test lives in; retention of a century, so that nothing real is dropped.
	if _, err := shared.system.Exec(context.Background(), `SELECT maintain_idempotency_partitions($1, 4, interval '100 years')`, clk.Now()); err != nil {
		t.Fatal(err)
	}
	status1(t, ingestOne(t, svc, tc, event("old-1", "10", clk.Now().Add(-time.Hour))), ingestv1.EventResult_STATUS_ACCEPTED, "")

	clk.Set(time.Date(2031, 5, 20, 12, 0, 0, 0, time.UTC)) // two months on: March is still among the three buckets
	if _, err := shared.system.Exec(context.Background(), `SELECT maintain_idempotency_partitions($1, 4, interval '100 years')`, clk.Now()); err != nil {
		t.Fatal(err)
	}
	status1(t, ingestAs(t, svc, tc, "k", correct("fix-1", "old-1", "9"))[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	// The correction lands in March's period, though it is 71 days later: it is late usage for the aggregator to bill, not an error here.
	if e := outboxEvents(t, tc.id); len(e) != 2 || e[1].PeriodId != "2031-03" || e[1].QuantityUnits != -1000 {
		t.Fatalf("outbox = %v", e)
	}

	clk.Set(time.Date(2031, 6, 1, 12, 0, 0, 0, time.UTC)) // three months on: March is out of reach
	if _, err := shared.system.Exec(context.Background(), `SELECT maintain_idempotency_partitions($1, 4, interval '100 years')`, clk.Now()); err != nil {
		t.Fatal(err)
	}
	status1(t, ingestAs(t, svc, tc, "k", correct("fix-2", "old-1", "8"))[0], ingestv1.EventResult_STATUS_REJECTED, "CORRECTION_UNKNOWN_EVENT")
}
