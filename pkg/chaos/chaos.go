//go:build chaos

// Package chaos builds the fault hooks of the correctness suite (make chaos). Only binaries built with the chaos tag
// contain the real hooks; without it Crash and Fail return nil, so a hook field stays unset.
package chaos

import (
	"errors"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
)

// probability reads the probability in [0,1] from the environment variable env. ok is false when it is unset or invalid
// (an invalid value is logged and ignored).
func probability(env string) (p float64, ok bool) {
	raw := os.Getenv(env)
	if raw == "" {
		return 0, false
	}
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil || p < 0 || p > 1 {
		slog.Error(env+" must be a probability in [0,1]; ignoring", "value", raw)
		return 0, false
	}
	return p, true
}

// Crash returns a hook that, with the probability in env, kills the process (exit 137, like an OOM kill) after logging
// crashMsg. armedMsg is logged once when the hook is armed. nil when env is unset or invalid.
func Crash(env, armedMsg, crashMsg string) func() error {
	p, ok := probability(env)
	if !ok {
		return nil
	}
	slog.Warn(armedMsg, "probability", p)
	return func() error {
		if rand.Float64() < p {
			slog.Error(crashMsg)
			os.Exit(137)
		}
		return nil
	}
}

// Fail returns a hook that, with the probability in env, returns an error with the text failMsg. armedMsg is logged once
// when the hook is armed. nil when env is unset or invalid.
func Fail(env, armedMsg, failMsg string) func() error {
	p, ok := probability(env)
	if !ok {
		return nil
	}
	slog.Warn(armedMsg, "probability", p)
	return func() error {
		if rand.Float64() < p {
			return errors.New(failMsg)
		}
		return nil
	}
}
