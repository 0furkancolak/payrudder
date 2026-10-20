// Package memlimit tells the Go runtime how much memory the container allows, so that the garbage collector works harder as the limit nears instead of
// the process being killed for it. A service that ran with the default settings would grow its heap towards the limit and get OOM-killed; with a soft
// limit a little under the container's, it collects instead. An explicit GOMEMLIMIT always wins.
package memlimit

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// Headroom is the share of the container limit the heap may use; the rest is for stacks, the runtime and memory the collector cannot see.
const Headroom = 0.9

// cgroupFiles are where a container's memory limit is: cgroup v2, then v1.
var cgroupFiles = []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"}

// Apply sets the runtime's soft memory limit from the container's cgroup limit. It reports the limit it set, or 0 when it set none (GOMEMLIMIT is set,
// there is no cgroup limit, or the limit is "max"/unreasonably large).
func Apply() int64 {
	if os.Getenv("GOMEMLIMIT") != "" {
		return 0
	}
	for _, f := range cgroupFiles {
		b, err := os.ReadFile(f) //nolint:gosec // a fixed kernel path
		if err != nil {
			continue
		}
		if limit := soft(string(b)); limit > 0 {
			debug.SetMemoryLimit(limit)
			return limit
		}
	}
	return 0
}

// soft is the soft limit for the text of a cgroup limit file: 0 for "max", an unparsable value or a limit above 1 TiB (v1's "no limit" is a huge number).
func soft(text string) int64 {
	text = strings.TrimSpace(text)
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n <= 0 || n > 1<<40 {
		return 0
	}
	return int64(float64(n) * Headroom)
}
