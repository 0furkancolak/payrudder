//go:build chaos

package main

import (
	"errors"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
)

// afterPublishFault implements the RELAY_CRASH_AFTER_PRODUCE fault hook: with the given probability the relay
// "crashes" after Kafka acked and before the outbox rows are deleted, so they are published again.
// Only chaos builds contain it.
func afterPublishFault() func() error {
	raw := os.Getenv("RELAY_CRASH_AFTER_PRODUCE")
	if raw == "" {
		return nil
	}
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil || p < 0 || p > 1 {
		slog.Error("RELAY_CRASH_AFTER_PRODUCE must be a probability in [0,1]; ignoring", "value", raw)
		return nil
	}
	slog.Warn("chaos build: relay will crash after produce", "probability", p)
	return func() error {
		if rand.Float64() < p {
			return errors.New("chaos: simulated relay crash after produce")
		}
		return nil
	}
}
