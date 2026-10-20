package memlimit

import "testing"

func TestSoft(t *testing.T) {
	for in, want := range map[string]int64{
		"134217728\n":         120795955, // 128 MiB -> 90 %
		"max\n":               0,
		"":                    0,
		"nonsense":            0,
		"0":                   0,
		"9223372036854771712": 0, // cgroup v1 without a limit
		"2199023255552":       0, // above 1 TiB
	} {
		if got := soft(in); got != want {
			t.Errorf("soft(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestExplicitLimitWins(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "64MiB")
	if Apply() != 0 {
		t.Fatal("GOMEMLIMIT is set: nothing may be overridden")
	}
}
