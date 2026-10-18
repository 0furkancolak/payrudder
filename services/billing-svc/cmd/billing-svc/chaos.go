//go:build chaos

package main

import (
	"errors"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
)

// afterPublishFault implements the BILLING_RELAY_CRASH_AFTER_PUBLISH fault hook: with the given probability the relay
// "crashes" after the broker acked and before the outbox rows are deleted, so they are published again.
// Only chaos builds contain it.
func afterPublishFault() func() error {
	raw := os.Getenv("BILLING_RELAY_CRASH_AFTER_PUBLISH")
	if raw == "" {
		return nil
	}
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil || p < 0 || p > 1 {
		slog.Error("BILLING_RELAY_CRASH_AFTER_PUBLISH must be a probability in [0,1]; ignoring", "value", raw)
		return nil
	}
	slog.Warn("chaos build: billing relay will crash after publish", "probability", p)
	return func() error {
		if rand.Float64() < p {
			return errors.New("chaos: simulated relay crash after produce")
		}
		return nil
	}
}
