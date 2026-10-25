package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func ev(mut func(*ingestv1.IncomingEvent)) *ingestv1.IncomingEvent {
	e := &ingestv1.IncomingEvent{
		IdempotencyKey: "req-1", CustomerExternalId: "cus_1", MeterCode: "gb_hours", Quantity: "1.5",
		EventTime: timestamppb.New(now.Add(-time.Hour)),
	}
	if mut != nil {
		mut(e)
	}
	return e
}

func TestCheckPrelim(t *testing.T) {
	manyProps := map[string]string{}
	for i := 0; i < 33; i++ {
		manyProps[fmt.Sprintf("k%d", i)] = "v"
	}
	cases := []struct {
		name string
		env  string
		ev   *ingestv1.IncomingEvent
		want string // error code, "" = ok
	}{
		{"valid live", "live", ev(nil), ""},
		{"valid sandbox without key", "sandbox", ev(func(e *ingestv1.IncomingEvent) { e.IdempotencyKey = "" }), ""},
		{"live without key", "live", ev(func(e *ingestv1.IncomingEvent) { e.IdempotencyKey = "" }), CodeKeyRequired},
		{"key with space", "live", ev(func(e *ingestv1.IncomingEvent) { e.IdempotencyKey = "a b" }), CodeKeyInvalid},
		{"key too long", "live", ev(func(e *ingestv1.IncomingEvent) { e.IdempotencyKey = strings.Repeat("a", 129) }), CodeKeyInvalid},
		{"key at max length", "live", ev(func(e *ingestv1.IncomingEvent) { e.IdempotencyKey = strings.Repeat("a", 128) }), ""},
		{"missing event time", "live", ev(func(e *ingestv1.IncomingEvent) { e.EventTime = nil }), CodeInvalidEventTime},
		{"too old", "live", ev(func(e *ingestv1.IncomingEvent) { e.EventTime = timestamppb.New(now.Add(-MaxLateness - time.Second)) }), CodeEventTooOld},
		{"exactly at max lateness", "live", ev(func(e *ingestv1.IncomingEvent) { e.EventTime = timestamppb.New(now.Add(-MaxLateness)) }), ""},
		{"future beyond skew", "live", ev(func(e *ingestv1.IncomingEvent) { e.EventTime = timestamppb.New(now.Add(MaxFuture + time.Second)) }), CodeEventInFuture},
		{"future within skew", "live", ev(func(e *ingestv1.IncomingEvent) { e.EventTime = timestamppb.New(now.Add(MaxFuture)) }), ""},
		{"too many properties", "live", ev(func(e *ingestv1.IncomingEvent) { e.Properties = manyProps }), CodeInvalidProperties},
		{"properties too large", "live", ev(func(e *ingestv1.IncomingEvent) { e.Properties = map[string]string{"k": strings.Repeat("x", 4096)} }), CodeInvalidProperties},
		{"empty property name", "live", ev(func(e *ingestv1.IncomingEvent) { e.Properties = map[string]string{"": "x"} }), CodeInvalidProperties},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, rej := CheckPrelim(c.ev, now, c.env)
			switch {
			case c.want == "" && rej != nil:
				t.Fatalf("unexpected rejection %v", rej)
			case c.want != "" && (rej == nil || rej.Code != c.want):
				t.Fatalf("got %v, want %s", rej, c.want)
			case c.want == "" && c.ev.IdempotencyKey == "" && !p.Auto:
				t.Fatal("sandbox event without a key must be Auto")
			}
		})
	}
}

func TestParseQuantity(t *testing.T) {
	cases := []struct {
		q     string
		scale int
		units int64
		code  string
	}{
		{"12", 0, 12, ""},
		{"1.25", 3, 1250, ""},
		{"0", 2, 0, ""},
		{"1.2", 0, 0, CodeInvalidPrecision},
		{"1.2345", 3, 0, CodeInvalidPrecision},
		{"1.2300", 2, 123, ""}, // trailing zeros are not extra precision
		{"-1", 2, 0, CodeInvalidQuantity},
		{"", 0, 0, CodeInvalidQuantity},
		{"abc", 0, 0, CodeInvalidQuantity},
		{"1e3", 0, 0, CodeInvalidQuantity},
		{".5", 1, 0, CodeInvalidQuantity},
		{"9223372036854775808", 0, 0, CodeInvalidQuantity}, // int64 overflow
		{"9223372036854776", 3, 0, CodeInvalidQuantity},    // overflows once scaled
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q@%d", c.q, c.scale), func(t *testing.T) {
			got, rej := ParseQuantity(c.q, c.scale)
			if c.code == "" {
				if rej != nil || got != c.units {
					t.Fatalf("got %d, %v; want %d", got, rej, c.units)
				}
				return
			}
			if rej == nil || rej.Code != c.code {
				t.Fatalf("got %v, want %s", rej, c.code)
			}
		})
	}
}

func TestPayloadHash(t *testing.T) {
	tm := now
	base := PayloadHash("cus_1", "m", 1500, 3, tm, map[string]string{"a": "1", "b": "2"})
	same := PayloadHash("cus_1", "m", 1500, 3, tm, map[string]string{"b": "2", "a": "1"})
	if string(base) != string(same) {
		t.Fatal("hash depends on property order")
	}
	for name, h := range map[string][]byte{
		"customer": PayloadHash("cus_2", "m", 1500, 3, tm, map[string]string{"a": "1", "b": "2"}),
		"meter":    PayloadHash("cus_1", "n", 1500, 3, tm, map[string]string{"a": "1", "b": "2"}),
		"units":    PayloadHash("cus_1", "m", 1501, 3, tm, map[string]string{"a": "1", "b": "2"}),
		"scale":    PayloadHash("cus_1", "m", 1500, 2, tm, map[string]string{"a": "1", "b": "2"}),
		"time":     PayloadHash("cus_1", "m", 1500, 3, tm.Add(time.Nanosecond), map[string]string{"a": "1", "b": "2"}),
		"prop":     PayloadHash("cus_1", "m", 1500, 3, tm, map[string]string{"a": "1", "b": "3"}),
		"extra":    PayloadHash("cus_1", "m", 1500, 3, tm, map[string]string{"a": "1", "b": "2", "c": "3"}),
		// Length prefixes keep ("ab","c") and ("a","bc") apart.
		"boundary": PayloadHash("cus_", "1m", 1500, 3, tm, map[string]string{"a": "1", "b": "2"}),
	} {
		if string(h) == string(base) {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
}

func TestBucketOf(t *testing.T) {
	got := BucketOf(time.Date(2026, 11, 30, 23, 59, 59, 0, time.FixedZone("x", 3*3600)))
	if !got.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket = %s", got)
	}
	// 2026-11-30 23:59 at UTC+3 is 20:59Z: still November. 2026-12-01 01:00 at UTC+3 is 22:00Z on Nov 30.
	got = BucketOf(time.Date(2026, 12, 1, 1, 0, 0, 0, time.FixedZone("x", 3*3600)))
	if !got.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket = %s, want UTC month", got)
	}
}
