// Package logx builds the structured JSON logger every Quotify process uses.
//
// Records always carry service and version. When the context holds a valid span, trace_id and span_id are added,
// so logs and traces can be joined in Grafana.
package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Options configures New.
type Options struct {
	Service string
	Version string
	Level   string // debug, info, warn, error
	Writer  io.Writer
}

// New returns a JSON logger with service metadata and trace correlation.
func New(opts Options) (*slog.Logger, error) {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return nil, err
	}
	base := slog.NewJSONHandler(opts.Writer, &slog.HandlerOptions{Level: level})
	h := &traceHandler{next: base}
	return slog.New(h).With(slog.String("service", opts.Service), slog.String("version", opts.Version)), nil
}

// ParseLevel maps a level name to a slog.Level. The empty string means info.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logx: unknown level %q", s)
	}
}

type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.next.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}

// Redacted wraps a secret so it never appears in logs. Use it for API keys, DSNs with passwords and tokens.
type Redacted string

// LogValue implements slog.LogValuer.
func (Redacted) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// String keeps fmt from printing the secret too.
func (Redacted) String() string { return "[REDACTED]" }
