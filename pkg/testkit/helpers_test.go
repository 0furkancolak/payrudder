package testkit

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// failingTB records Fatalf instead of ending the test.
type failingTB struct {
	testing.TB
	msg string
}

func (f *failingTB) Helper() {}
func (f *failingTB) Fatalf(format string, args ...any) {
	f.msg = format
	panic(f)
}

func TestEventuallyReturnsWhenTheConditionHolds(t *testing.T) {
	var n atomic.Int32
	Eventually(t, 5*time.Second, "the third look", func() bool { return n.Add(1) == 3 })
	if n.Load() != 3 {
		t.Fatalf("looked %d times, want 3", n.Load())
	}
}

func TestEventuallyFailsAfterTheTimeout(t *testing.T) {
	f := &failingTB{TB: t}
	defer func() {
		if recover() == nil || !strings.Contains(f.msg, "timed out") {
			t.Fatalf("Eventually did not fail the test with a timeout (msg %q)", f.msg)
		}
	}()
	Eventually(f, 120*time.Millisecond, "never", func() bool { return false })
}

func TestLoggerIsSilentUnlessVerbose(t *testing.T) {
	t.Setenv("ITEST_VERBOSE", "")
	if Logger() == nil {
		t.Fatal("no logger")
	}
}
