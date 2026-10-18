//go:build chaos

package main

import (
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
)

// afterCommitFault implements the AGG_CRASH_AFTER_COMMIT fault hook: with the given probability per batch the process
// dies after the batch committed and before Kafka heard about it, exactly like a crash at that point. On restart the
// consumer resumes from the database offset. Only chaos builds contain it.
func afterCommitFault() func() error {
	raw := os.Getenv("AGG_CRASH_AFTER_COMMIT")
	if raw == "" {
		return nil
	}
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil || p < 0 || p > 1 {
		slog.Error("AGG_CRASH_AFTER_COMMIT must be a probability in [0,1]; ignoring", "value", raw)
		return nil
	}
	slog.Warn("chaos build: aggregator will crash after commit", "probability", p)
	return func() error {
		if rand.Float64() < p {
			slog.Error("chaos: crashing after commit")
			os.Exit(137)
		}
		return nil
	}
}
