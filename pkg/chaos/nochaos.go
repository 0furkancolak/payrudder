//go:build !chaos

// Package chaos builds the fault hooks of the correctness suite (make chaos). Only binaries built with the chaos tag
// contain the real hooks; without it Crash and Fail return nil, so a hook field stays unset.
package chaos

// Crash is the no-op of normal builds.
func Crash(_, _, _ string) func() error { return nil }

// Fail is the no-op of normal builds.
func Fail(_, _, _ string) func() error { return nil }
