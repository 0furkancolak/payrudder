package backoff

import (
	"context"
	"log/slog"
)

type countHandler struct{ warns, infos int }

func (h *countHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countHandler) Handle(_ context.Context, r slog.Record) error {
	switch r.Level {
	case slog.LevelWarn:
		h.warns++
	case slog.LevelInfo:
		h.infos++
	}
	return nil
}
func (h *countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countHandler) WithGroup(string) slog.Handler      { return h }

func newLog(h slog.Handler) *slog.Logger { return slog.New(h) }
