//go:build integration

// Package itest runs analytics-svc against real ClickHouse and Redpanda.
package itest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/api"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/chmigrate"
	"github.com/0furkancolak/payrudder/services/analytics-svc/internal/consumer"
	"github.com/0furkancolak/payrudder/services/analytics-svc/migrations"
)

type world struct {
	ch       testkit.ClickHouse
	conn     driver.Conn
	brokers  string
	producer *kgo.Client
	svc      *api.Server
}

func start(t *testing.T) *world {
	t.Helper()
	w := &world{ch: testkit.StartClickHouse(t), brokers: testkit.StartRedpanda(t)}
	var err error
	w.conn, err = clickhouse.Open(chmigrate.Options(w.ch.Addr, w.ch.Database, w.ch.User, w.ch.Password))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.conn.Close() })
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if err = chmigrate.Run(ctx, w.conn, migrations.FS); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := chmigrate.Run(ctx, w.conn, migrations.FS); err != nil { // idempotent
		t.Fatalf("second migrate run: %v", err)
	}
	admin, _ := kgo.NewClient(kgo.SeedBrokers(w.brokers))
	if _, err := kadm.NewClient(admin).CreateTopics(ctx, 3, 1, nil, consumer.UsageTopic, consumer.LimitTopic, consumer.InvoiceTopic,
		consumer.UsageDLQ, consumer.LimitDLQ, consumer.InvoiceDLQ); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	if w.producer, err = kafkax.NewProducer([]string{w.brokers}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.producer.Close)

	r, err := consumer.New(consumer.Config{Brokers: []string{w.brokers}, Group: "analytics-test", Conn: w.conn, Producer: w.producer,
		BatchRows: 2000, BatchWait: 300 * time.Millisecond, Backoff: 200 * time.Millisecond, Log: testkit.Logger()})
	if err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(rctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	w.svc = api.New(w.conn)
	return w
}

func event(tn, customer uuid.UUID, id string, meter string, units int64, scale uint32, at time.Time) []byte {
	b, _ := proto.Marshal(&eventsv1.UsageEvent{EventId: id, TenantId: tn.String(), IdempotencyKey: id, CustomerId: customer.String(), CustomerExternalId: "cus_" + customer.String()[:4],
		MeterCode: meter, EventTime: timestamppb.New(at), IngestedAt: timestamppb.New(at.Add(time.Second)), QuantityUnits: units, QuantityScale: scale,
		PeriodId: at.UTC().Format("2006-01"), MeterVersion: 1, Properties: map[string]string{"region": "eu"}})
	return b
}

func (w *world) produce(t *testing.T, recs ...*kgo.Record) {
	t.Helper()
	for i := 0; i < len(recs); i += 5000 {
		if err := w.producer.ProduceSync(context.Background(), recs[i:min(i+5000, len(recs))]...).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
}

func rawRows(t *testing.T, w *world, tn uuid.UUID) uint64 {
	t.Helper()
	var n uint64
	if err := w.conn.QueryRow(context.Background(), `SELECT count() FROM usage_events WHERE tenant_id = ?`, tn).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ctxOf(tn uuid.UUID) context.Context {
	return tenant.NewContext(context.Background(), tenant.Info{ID: tn, Timezone: "UTC"})
}

func TestAnalytics(t *testing.T) {
	w := start(t)
	base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)

	t.Run("DuplicateDeliveriesDoNotChangeExactTotals", func(t *testing.T) {
		tn := uuid.New()
		customers := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
		rng := rand.New(rand.NewPCG(7, 11))
		type key struct {
			c uuid.UUID
			m string
		}
		want := map[key]int64{}
		var recs []*kgo.Record
		const n = 10000
		for i := 0; i < n; i++ {
			c, m := customers[rng.IntN(3)], []string{"api", "gb"}[rng.IntN(2)]
			units := int64(1 + rng.IntN(1000))
			at := base.Add(time.Duration(rng.IntN(90)) * time.Minute)
			rec := &kgo.Record{Topic: consumer.UsageTopic, Key: []byte(tn.String()), Value: event(tn, c, fmt.Sprintf("ev%d", i), m, units, 3, at)}
			recs = append(recs, rec)
			want[key{c, m}] += units
			if rng.IntN(100) < 5 { // a duplicate delivery of the same event
				recs = append(recs, &kgo.Record{Topic: consumer.UsageTopic, Key: rec.Key, Value: rec.Value})
			}
		}
		dups := uint64(len(recs) - n)
		w.produce(t, recs...)
		// Background merges may already have collapsed some duplicates, so wait on the deduplicated count.
		testkit.Eventually(t, 90*time.Second, "all unique events inserted", func() bool {
			var u uint64
			return w.conn.QueryRow(context.Background(), `SELECT count() FROM usage_events FINAL WHERE tenant_id = ?`, tn).Scan(&u) == nil && u == n
		})

		// FINAL (deduplicated) totals are exactly the aggregator's: every unique event once.
		rows, err := w.conn.Query(context.Background(), `SELECT customer_id, meter_code, sum(quantity_units) FROM usage_events FINAL WHERE tenant_id = ? GROUP BY customer_id, meter_code`, tn)
		if err != nil {
			t.Fatal(err)
		}
		got := map[key]int64{}
		for rows.Next() {
			var c uuid.UUID
			var m string
			var u int64
			if err := rows.Scan(&c, &m, &u); err != nil {
				t.Fatal(err)
			}
			got[key{c, m}] = u
		}
		rows.Close()
		if len(got) != len(want) {
			t.Fatalf("FINAL has %d groups, want %d", len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("FINAL total of %v = %d, want %d", k, got[k], v)
			}
		}

		// The hourly rollup sees duplicates before merges: measure the drift against FINAL (reported, never asserted zero).
		var hourly, final int64
		_ = w.conn.QueryRow(context.Background(), `SELECT sum(quantity_units_sum) FROM usage_hourly WHERE tenant_id = ?`, tn).Scan(&hourly)
		for _, v := range want {
			final += v
		}
		t.Logf("rollup drift: usage_hourly %d vs exact %d (+%d, %.2f%%) over %d duplicate deliveries", hourly, final, hourly-final, 100*float64(hourly-final)/float64(final), dups)
		if hourly < final {
			t.Fatalf("the rollup under-counted: %d < %d", hourly, final)
		}

		// The API: the estimated series sums to the rollup, the exact one (a day at most) to FINAL.
		est, err := w.svc.QueryUsage(ctxOf(tn), &analyticsv1.QueryUsageRequest{MeterCode: "api", From: timestamppb.New(base.Add(-time.Hour)), To: timestamppb.New(base.Add(4 * time.Hour)),
			Granularity: analyticsv1.Granularity_GRANULARITY_HOUR})
		if err != nil || est.GetPrecision() != "estimated" || len(est.GetPoints()) == 0 || est.GetQuantityScale() != 3 {
			t.Fatalf("estimated series = %v, %v", est, err)
		}
		exact, err := w.svc.QueryUsage(ctxOf(tn), &analyticsv1.QueryUsageRequest{MeterCode: "api", From: timestamppb.New(base.Add(-time.Hour)), To: timestamppb.New(base.Add(4 * time.Hour)),
			Granularity: analyticsv1.Granularity_GRANULARITY_HOUR, Exact: true})
		if err != nil || exact.GetPrecision() != "exact" {
			t.Fatalf("exact series = %v, %v", exact, err)
		}
		var apiWant int64
		for k, v := range want {
			if k.m == "api" {
				apiWant += v
			}
		}
		var sum int64
		var events int64
		for _, p := range exact.GetPoints() {
			var whole, frac int64
			fmt.Sscanf(p.GetQuantity(), "%d.%d", &whole, &frac)
			sum += whole*1000 + frac
			events += p.GetEventCount()
		}
		if sum != apiWant {
			t.Fatalf("exact series sums to %d, want %d", sum, apiWant)
		}
		// More than a day cannot be asked exactly.
		_, err = w.svc.QueryUsage(ctxOf(tn), &analyticsv1.QueryUsageRequest{MeterCode: "api", From: timestamppb.New(base.Add(-72 * time.Hour)), To: timestamppb.New(base.Add(time.Hour)), Exact: true})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("exact over 3 days: %v, want INVALID_ARGUMENT", err)
		}
		// Top customers (exact, FINAL) match the model.
		top, err := w.svc.TopCustomers(ctxOf(tn), &analyticsv1.TopCustomersRequest{MeterCode: "api", PeriodId: base.Format("2006-01"), Limit: 2})
		if err != nil || len(top.GetCustomers()) != 2 {
			t.Fatalf("top = %v, %v", top, err)
		}
	})

	t.Run("TenantsDoNotSeeEachOther", func(t *testing.T) {
		a, b := uuid.New(), uuid.New()
		ca, cb := uuid.New(), uuid.New()
		at := base.Add(10 * time.Minute)
		w.produce(t,
			&kgo.Record{Topic: consumer.UsageTopic, Key: []byte(a.String()), Value: event(a, ca, "iso-a", "api", 7000, 3, at)},
			&kgo.Record{Topic: consumer.UsageTopic, Key: []byte(b.String()), Value: event(b, cb, "iso-b", "api", 99000, 3, at)})
		testkit.Eventually(t, 30*time.Second, "both rows", func() bool { return rawRows(t, w, a) == 1 && rawRows(t, w, b) == 1 })
		req := &analyticsv1.QueryUsageRequest{MeterCode: "api", From: timestamppb.New(base.Add(-time.Hour)), To: timestamppb.New(base.Add(4 * time.Hour))}
		ra, err := w.svc.QueryUsage(ctxOf(a), req)
		if err != nil || len(ra.GetPoints()) != 1 || ra.GetPoints()[0].GetQuantity() != "7.000" {
			t.Fatalf("tenant A sees %v, %v; want only its own 7.000", ra, err)
		}
		// A asking for B's customer gets nothing.
		req.CustomerId = cb.String()
		ra, err = w.svc.QueryUsage(ctxOf(a), req)
		if err != nil || len(ra.GetPoints()) != 0 {
			t.Fatalf("tenant A asking for B's customer got %v, %v", ra, err)
		}
		top, err := w.svc.TopCustomers(ctxOf(a), &analyticsv1.TopCustomersRequest{MeterCode: "api", PeriodId: base.Format("2006-01"), Limit: 10})
		if err != nil || len(top.GetCustomers()) != 1 || top.GetCustomers()[0].GetCustomerId() != ca.String() {
			t.Fatalf("top customers of A = %v, %v", top, err)
		}
		// Ingest stats are operator-only.
		if _, err := w.svc.GetIngestStats(ctxOf(a), &analyticsv1.GetIngestStatsRequest{}); err == nil {
			t.Fatal("a tenant read the operator ingest stats")
		}
		stats, err := w.svc.GetIngestStats(tenant.WithOperator(context.Background()), &analyticsv1.GetIngestStatsRequest{WindowSeconds: 4 * 3600})
		if err != nil || len(stats.GetTenants()) < 2 {
			t.Fatalf("operator stats = %v, %v", stats, err)
		}
	})

	t.Run("UndecodableRecordGoesToTheDLQ", func(t *testing.T) {
		w.produce(t, &kgo.Record{Topic: consumer.UsageTopic, Key: []byte("x"), Value: []byte{0xff, 0xff, 0x00}},
			&kgo.Record{Topic: consumer.UsageTopic, Key: []byte("x"), Value: []byte{0x0a, 0x01, 'x'}, Headers: []kgo.RecordHeader{{Key: "x-redrive-target", Value: []byte("aggregator")}}})
		cl, _ := kgo.NewClient(kgo.SeedBrokers(w.brokers), kgo.ConsumeTopics(consumer.UsageDLQ), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
		defer cl.Close()
		var dead []*kgo.Record
		deadline := time.Now().Add(30 * time.Second)
		for len(dead) == 0 && time.Now().Before(deadline) {
			pctx, cancel := context.WithTimeout(context.Background(), time.Second)
			cl.PollFetches(pctx).EachRecord(func(r *kgo.Record) { dead = append(dead, r) })
			cancel()
		}
		if len(dead) != 1 {
			t.Fatalf("%d dead letters, want 1 (the redrive aimed at the aggregator is skipped, not dead-lettered)", len(dead))
		}
		consumerHeader := ""
		for _, h := range dead[0].Headers {
			if h.Key == "x-dlq-consumer" {
				consumerHeader = string(h.Value)
			}
		}
		if consumerHeader != "analytics-test" {
			t.Fatalf("x-dlq-consumer = %q", consumerHeader)
		}
	})

	t.Run("ClickHouseDownForThirtySecondsLosesNothing", func(t *testing.T) {
		tn := uuid.New()
		c := uuid.New()
		if out, err := exec.Command("docker", "pause", w.ch.ContainerID).CombinedOutput(); err != nil {
			t.Fatalf("docker pause: %v %s", err, out)
		}
		unpaused := false
		unpause := func() {
			if !unpaused {
				unpaused = true
				_ = exec.Command("docker", "unpause", w.ch.ContainerID).Run()
			}
		}
		defer unpause()
		var recs []*kgo.Record
		var want int64
		for i := 0; i < 3000; i++ {
			units := int64(i%97 + 1)
			want += units
			recs = append(recs, &kgo.Record{Topic: consumer.UsageTopic, Key: []byte(tn.String()), Value: event(tn, c, fmt.Sprintf("down%d", i), "api", units, 0, base.Add(time.Duration(i%60)*time.Minute))})
		}
		w.produce(t, recs...)
		time.Sleep(30 * time.Second) // the consumer retries meanwhile; its offsets stay uncommitted
		unpause()
		testkit.Eventually(t, 90*time.Second, "the backlog to drain", func() bool {
			var n uint64
			return w.conn.QueryRow(context.Background(), `SELECT count() FROM usage_events FINAL WHERE tenant_id = ?`, tn).Scan(&n) == nil && n == 3000
		})
		var got int64
		if err := w.conn.QueryRow(context.Background(), `SELECT sum(quantity_units) FROM usage_events FINAL WHERE tenant_id = ?`, tn).Scan(&got); err != nil || got != want {
			t.Fatalf("total after recovery = %d, %v; want %d", got, err, want)
		}
	})
}
