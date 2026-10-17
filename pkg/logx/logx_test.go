package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/quotify/pkg/logx"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	return m
}

func TestServiceAndTraceFields(t *testing.T) {
	var buf bytes.Buffer
	log, err := logx.New(logx.Options{Service: "hello", Version: "v1", Level: "info", Writer: &buf})
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))

	log.InfoContext(ctx, "hi")
	m := decode(t, &buf)
	for k, want := range map[string]string{
		"service": "hello", "version": "v1", "trace_id": tid.String(), "span_id": sid.String(),
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %s", k, m[k], want)
		}
	}
}

func TestNoTraceFieldsWithoutSpan(t *testing.T) {
	var buf bytes.Buffer
	log, _ := logx.New(logx.Options{Service: "s", Writer: &buf})
	log.Info("hi")
	if _, ok := decode(t, &buf)["trace_id"]; ok {
		t.Fatal("trace_id present without a span")
	}
}

func TestRedacted(t *testing.T) {
	var buf bytes.Buffer
	log, _ := logx.New(logx.Options{Service: "s", Writer: &buf})
	secret := logx.Redacted("qk_live_abcdefgh_supersecret")
	log.Info("key", "api_key", secret)
	if got := decode(t, &buf)["api_key"]; got != "[REDACTED]" {
		t.Fatalf("api_key = %v", got)
	}
	if s := fmt.Sprint(secret); s != "[REDACTED]" {
		t.Fatalf("fmt printed %q", s)
	}
}

func TestLevel(t *testing.T) {
	var buf bytes.Buffer
	log, _ := logx.New(logx.Options{Service: "s", Level: "warn", Writer: &buf})
	log.Info("dropped")
	if buf.Len() != 0 {
		t.Fatalf("info logged at warn level: %q", buf.String())
	}
	if _, err := logx.ParseLevel("loud"); err == nil {
		t.Fatal("ParseLevel accepted an unknown level")
	}
}
