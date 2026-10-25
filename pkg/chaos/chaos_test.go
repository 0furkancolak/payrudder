//go:build chaos

package chaos_test

import (
	"testing"

	"github.com/0furkancolak/payrudder/pkg/chaos"
)

func TestFailHonoursProbability(t *testing.T) {
	t.Setenv("CHAOS_T", "1")
	if err := chaos.Fail("CHAOS_T", "armed", "boom")(); err == nil || err.Error() != "boom" {
		t.Fatalf("p=1 err = %v, want boom", err)
	}
	t.Setenv("CHAOS_T", "0")
	if err := chaos.Fail("CHAOS_T", "armed", "boom")(); err != nil {
		t.Fatalf("p=0 err = %v, want nil", err)
	}
}

func TestUnsetOrInvalidIsNil(t *testing.T) {
	t.Setenv("CHAOS_T", "")
	if chaos.Crash("CHAOS_T", "a", "c") != nil || chaos.Fail("CHAOS_T", "a", "f") != nil {
		t.Fatal("unset env must give a nil hook")
	}
	for _, bad := range []string{"x", "-0.1", "1.5"} {
		t.Setenv("CHAOS_T", bad)
		if chaos.Crash("CHAOS_T", "a", "c") != nil {
			t.Fatalf("%q must give a nil hook", bad)
		}
	}
}
