//go:build chaos

package main

import (
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
)

// afterLuaFault implements the LIMITS_CRASH_AFTER_LUA fault hook: with the given probability per batch the process dies
// after the counters and the offset were committed and before thresholds were evaluated. On restart the batch is not
// redelivered (the offset is in Redis) and the reconciliation sweep fires the missed thresholds. Only chaos builds
// contain it.
func afterLuaFault() func() error {
	raw := os.Getenv("LIMITS_CRASH_AFTER_LUA")
	if raw == "" {
		return nil
	}
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil || p < 0 || p > 1 {
		slog.Error("LIMITS_CRASH_AFTER_LUA must be a probability in [0,1]; ignoring", "value", raw)
		return nil
	}
	slog.Warn("chaos build: limits-svc will crash after the script", "probability", p)
	return func() error {
		if rand.Float64() < p {
			slog.Error("chaos: crashing after the script")
			os.Exit(137)
		}
		return nil
	}
}
