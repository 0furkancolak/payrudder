// Package contract holds wire-compatibility tests: golden protobuf fixtures that every release must keep decoding
// (docs/09-testing-strategy.md, ADR 0007).
package contract

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/quotify/gen/go/quotify/events/v1"
)

var update = flag.Bool("update", false, "rewrite the golden fixtures of the current release (only for a new release tag)")

// usageEventV010 is the reference message of release v0.1.0. Never edit it: add a new fixture for a new release.
func usageEventV010() *eventsv1.UsageEvent {
	return &eventsv1.UsageEvent{
		EventId:            "01JD8Z3Q5K7M9N2P4R6T8V0W1X",
		TenantId:           "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e",
		IdempotencyKey:     "req_8f2c",
		CustomerId:         "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d60",
		CustomerExternalId: "cus_42",
		MeterCode:          "gb_hours",
		EventTime:          timestamppb.New(time.Date(2026, 11, 3, 10, 15, 0, 0, time.UTC)),
		IngestedAt:         timestamppb.New(time.Date(2026, 11, 3, 10, 15, 1, 500_000_000, time.UTC)),
		Properties:         map[string]string{"region": "eu", "model": "gpt-x"},
		QuantityUnits:      1500,
		QuantityScale:      3,
		PeriodId:           "2026-11",
		MeterVersion:       2,
	}
}

func TestUsageEventGoldenV010(t *testing.T) {
	path := filepath.Join("..", "..", "proto", "testdata", "UsageEvent", "v0.1.0.bin")
	want := usageEventV010()
	if *update {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden fixture missing (run with -update once): %v", err)
	}

	// Every release must decode the fixture of every earlier release...
	got := &eventsv1.UsageEvent{}
	if err := proto.Unmarshal(raw, got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("decoded fixture differs from the reference message:\n got: %v\nwant: %v", got, want)
	}
	// ...and must keep producing the same bytes for the same message, so a field renumbering shows up here.
	again, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, raw) {
		t.Fatalf("encoding of the reference message changed (%d bytes, fixture has %d): this breaks consumers", len(again), len(raw))
	}
}
