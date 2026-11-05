//go:build integration

package itest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/grpcapi"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/maintenance"
)

// rejectedCount is the number of rows of the tenant's log, whatever their age.
func rejectedCount(t *testing.T, tc tenantCtx) int {
	t.Helper()
	var n int
	err := pgxx.InTenantTx(context.Background(), shared.appDB, tc.id, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count(*) FROM rejected_events`).Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// waitRejected waits for the (asynchronous) log writer until the tenant's list holds want rows.
func waitRejected(t *testing.T, svc *grpcapi.Server, tc tenantCtx, want int) []*ingestv1.RejectedEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := svc.ListRejectedEvents(tc.ctx, &ingestv1.ListRejectedEventsRequest{PageSize: 200})
		if err != nil {
			t.Fatalf("ListRejectedEvents: %v", err)
		}
		if len(resp.Events) == want {
			return resp.Events
		}
		if time.Now().After(deadline) {
			t.Fatalf("the list holds %d rejected events, want %d", len(resp.Events), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// testRejectedEventLog: a rejection is logged without its properties, listed newest first with filters and a cursor, summarised, kept per tenant, and bounded by age and count.
func testRejectedEventLog(t *testing.T) {
	svc := newService(t, testOpts{})
	ctx := context.Background()
	a, b := newTenant("live"), newTenant("live")
	at := time.Now().UTC().Add(-time.Hour)

	if got := waitRejected(t, svc, a, 0); len(got) != 0 {
		t.Fatalf("a new tenant has %d rejected events", len(got))
	}
	ok := event("ok-1", "1", at)
	noMeter := event("bad-1", "1", at)
	noMeter.MeterCode = "nope"
	noMeter.Properties = map[string]string{"email": "secret@example.com"}
	noCustomer := event("bad-2", "1", at)
	noCustomer.CustomerExternalId = "ghost"
	badQty := event("bad-3", "x", at)
	resp, err := svc.IngestEvents(a.ctx, &ingestv1.IngestEventsRequest{Events: []*ingestv1.IncomingEvent{ok, noMeter, noCustomer, badQty}})
	if err != nil {
		t.Fatal(err)
	}
	status1(t, resp.Results[0], ingestv1.EventResult_STATUS_ACCEPTED, "")
	// One rejection of another tenant: it must never show in A's log.
	bad := event("other-1", "1", at)
	bad.MeterCode = "nope"
	status1(t, ingestOne(t, svc, b, bad), ingestv1.EventResult_STATUS_REJECTED, "UNKNOWN_METER")

	got := waitRejected(t, svc, a, 3)
	byKey := map[string]*ingestv1.RejectedEvent{}
	for _, e := range got {
		byKey[e.IdempotencyKey] = e
	}
	m := byKey["bad-1"]
	if m == nil || m.ErrorCode != "UNKNOWN_METER" || m.MeterCode != "nope" || m.CustomerExternalId != "cus_1" || m.ErrorMessage == "" || m.Id == "" ||
		m.EventTime.AsTime().Sub(at).Abs() > time.Second || time.Since(m.ReceivedTime.AsTime()) > time.Minute {
		t.Fatalf("logged row = %+v", m)
	}
	if byKey["bad-2"].GetErrorCode() != "UNKNOWN_CUSTOMER" || byKey["bad-2"].GetCustomerExternalId() != "ghost" || byKey["bad-3"].GetErrorCode() != "INVALID_QUANTITY" {
		t.Fatalf("logged rows = %+v", got)
	}
	// Newest first.
	for i := 1; i < len(got); i++ {
		if got[i].ReceivedTime.AsTime().After(got[i-1].ReceivedTime.AsTime()) {
			t.Fatalf("not newest first: %v", got)
		}
	}
	// Nothing of the payload is kept: the table has no column for it, and the properties' value is nowhere in a row.
	err = pgxx.InTenantTx(ctx, shared.appDB, a.id, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM rejected_events r WHERE r::text LIKE '%secret@example.com%'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("%d rows hold the properties", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Filters.
	list := func(req *ingestv1.ListRejectedEventsRequest) *ingestv1.ListRejectedEventsResponse {
		r, err := svc.ListRejectedEvents(a.ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := list(&ingestv1.ListRejectedEventsRequest{ErrorCode: "UNKNOWN_CUSTOMER"}); len(r.Events) != 1 || r.Events[0].IdempotencyKey != "bad-2" {
		t.Fatalf("by code = %v", r.Events)
	}
	if r := list(&ingestv1.ListRejectedEventsRequest{MeterCode: "nope"}); len(r.Events) != 1 || r.Events[0].IdempotencyKey != "bad-1" {
		t.Fatalf("by meter = %v", r.Events)
	}
	// Cursor: pages of two cover the three rows once.
	p1 := list(&ingestv1.ListRejectedEventsRequest{PageSize: 2})
	if len(p1.Events) != 2 || p1.NextPageToken == "" {
		t.Fatalf("page 1 = %d rows, token %q", len(p1.Events), p1.NextPageToken)
	}
	p2 := list(&ingestv1.ListRejectedEventsRequest{PageSize: 2, PageToken: p1.NextPageToken})
	if len(p2.Events) != 1 || p2.NextPageToken != "" || p2.Events[0].Id == p1.Events[0].Id || p2.Events[0].Id == p1.Events[1].Id {
		t.Fatalf("page 2 = %v", p2)
	}
	for _, bad := range []*ingestv1.ListRejectedEventsRequest{{PageToken: "junk"}, {PageSize: 201}, {PageSize: -1}} {
		if _, err := svc.ListRejectedEvents(a.ctx, bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("request %v: err = %v, want InvalidArgument", bad, err)
		}
	}

	// Summary: counts per code, and 24 hourly buckets that add up to the same total.
	s, err := svc.GetRejectedEventSummary(a.ctx, &ingestv1.GetRejectedEventSummaryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 3 || len(s.ByCode) != 3 || len(s.Hourly) != 24 {
		t.Fatalf("summary = %v", s)
	}
	var sum int32
	for _, h := range s.Hourly {
		sum += h.Rejected
	}
	if sum != 3 {
		t.Fatalf("hourly buckets add up to %d, want 3", sum)
	}

	// Tenant B sees its own row only.
	if gb := waitRejected(t, svc, b, 1); gb[0].IdempotencyKey != "other-1" {
		t.Fatalf("tenant B's log = %v", gb)
	}

	// Bounded by age: a row past 14 days is not listed and the sweep drops it.
	err = pgxx.InTenantTx(ctx, shared.appDB, a.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO rejected_events (tenant_id, error_code, error_message, meter_code, customer_external_id, idempotency_key, received_at)
			VALUES ($1, 'UNKNOWN_METER', 'old', 'nope', 'cus_1', 'old-1', now() - interval '15 days')`, a.id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := rejectedCount(t, a); n != 4 {
		t.Fatalf("rows = %d, want 4", n)
	}
	waitRejected(t, svc, a, 3)
	if _, _, err := maintenance.Once(ctx, shared.system, time.Now()); err != nil {
		t.Fatal(err)
	}
	if shared.router != nil {
		if _, _, err := maintenance.OnceCells(ctx, shared.router, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if n := rejectedCount(t, a); n != 3 {
		t.Fatalf("rows after the sweep = %d, want 3", n)
	}

	// Bounded by count: a tenant over 10 000 rows is cut back to its newest 10 000 by its next rejection (the first one of a tenant trims at once; later ones once a minute).
	c := newTenant("live")
	err = pgxx.InTenantTx(ctx, shared.appDB, c.id, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO rejected_events (tenant_id, error_code, error_message, meter_code, customer_external_id, idempotency_key, received_at)
			SELECT $1, 'UNKNOWN_METER', 'bulk', 'nope', 'cus_1', 'bulk-' || g, now() - make_interval(secs => g) FROM generate_series(1, 10050) AS g`, c.id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	bad2 := event("newest-1", "1", at)
	bad2.MeterCode = "nope"
	status1(t, ingestOne(t, svc, c, bad2), ingestv1.EventResult_STATUS_REJECTED, "UNKNOWN_METER")
	deadline := time.Now().Add(15 * time.Second)
	for rejectedCount(t, c) != 10_000 {
		if time.Now().After(deadline) {
			t.Fatalf("rows = %d, want 10000", rejectedCount(t, c))
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The newest are the ones kept.
	if first := list2(t, svc, c); first.IdempotencyKey != "newest-1" {
		t.Fatalf("newest row = %q, want newest-1", first.IdempotencyKey)
	}
}

func list2(t *testing.T, svc *grpcapi.Server, tc tenantCtx) *ingestv1.RejectedEvent {
	t.Helper()
	r, err := svc.ListRejectedEvents(tc.ctx, &ingestv1.ListRejectedEventsRequest{PageSize: 1})
	if err != nil || len(r.Events) != 1 {
		t.Fatalf("list = %v, %v", r, err)
	}
	return r.Events[0]
}
