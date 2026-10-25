package model

import (
	"testing"
	"time"
)

func TestCommissionAmount(t *testing.T) {
	pct := Program{Kind: KindPercent, Value: 1000}
	fixed := Program{Kind: KindFixed, Value: 500, Currency: "EUR"}
	for _, tc := range []struct {
		name     string
		p        Program
		currency string
		net      int64
		want     int64
		ok       bool
	}{
		{"ten percent", pct, "EUR", 10000, 1000, true},
		{"rounds half up", pct, "EUR", 15, 2, true},
		{"rounds down below half", pct, "EUR", 14, 1, true},
		{"a tiny invoice earns nothing", Program{Kind: KindPercent, Value: 100}, "EUR", 40, 0, false},
		{"nothing paid", pct, "EUR", 0, 0, false},
		{"a refund is not a sale", pct, "EUR", -100, 0, false},
		{"percent follows any currency", pct, "TRY", 20000, 2000, true},
		{"fixed in its currency", fixed, "EUR", 100, 500, true},
		{"fixed in lower case currency", fixed, "eur", 100, 500, true},
		{"fixed does not convert", fixed, "USD", 10000, 0, false},
		{"huge amounts do not overflow", Program{Kind: KindPercent, Value: 10000}, "EUR", 9_000_000_000_000_000_000, 9_000_000_000_000_000_000, true},
		{"unknown kind", Program{Kind: "x", Value: 1}, "EUR", 100, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := CommissionAmount(tc.p, tc.currency, tc.net)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("CommissionAmount = %d, %v; want %d, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestTermAllows(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		p     Program
		prior int64
		first *time.Time
		want  bool
	}{
		{"lifetime pays forever", Program{Term: TermLifetime}, 500, &first, true},
		{"first invoices: the third of three", Program{Term: TermFirstInvoices, TermValue: 3}, 2, &first, true},
		{"first invoices: the fourth of three", Program{Term: TermFirstInvoices, TermValue: 3}, 3, &first, false},
		{"months: still inside", Program{Term: TermMonths, TermValue: 6}, 5, &first, true},
		{"months: past the end", Program{Term: TermMonths, TermValue: 5}, 5, &first, false},
		{"months: the first invoice starts the clock", Program{Term: TermMonths, TermValue: 1}, 0, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TermAllows(tc.p, tc.prior, tc.first, now); got != tc.want {
				t.Fatalf("TermAllows = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWithinWindow(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		touched time.Time
		want    bool
	}{
		{"today", now, true},
		{"the last day", now.AddDate(0, 0, -30), true},
		{"a second past the window", now.AddDate(0, 0, -30).Add(-time.Second), false},
		{"in the future", now.Add(time.Hour), false},
		{"clock skew of seconds", now.Add(10 * time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WithinWindow(tc.touched, now, 30); got != tc.want {
				t.Fatalf("WithinWindow = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReversalTarget(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		amount, credited, total int64
		want                    int64
	}{
		{"credited in full", 1000, 12000, 12000, 1000},
		{"credited more than the total", 1000, 99999, 12000, 1000},
		{"half", 1000, 6000, 12000, 500},
		{"a third rounds", 1000, 4000, 12000, 333},
		{"two thirds rounds up", 1000, 8000, 12000, 667},
		{"nothing credited", 1000, 0, 12000, 0},
		{"no commission", 0, 6000, 12000, 0},
		{"free invoice", 1000, 5, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReversalTarget(tc.amount, tc.credited, tc.total); got != tc.want {
				t.Fatalf("ReversalTarget = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSelfReferral(t *testing.T) {
	for _, tc := range []struct {
		name   string
		aff    string
		cust   string
		affPM  []string
		custPM string
		want   string
	}{
		{"same mailbox", "ann@example.com", "ann@example.com", nil, "", ReasonSameEmail},
		{"same mailbox in another case", "Ann@Example.com", "ann@example.COM", nil, "", ReasonSameEmail},
		{"a plus tag is the same mailbox", "ann@example.com", "ann+shop@example.com", nil, "", ReasonSameEmail},
		{"the same company domain", "ann@acme.io", "bob@acme.io", nil, "", ReasonSameDomain},
		{"a shared provider is not a sign", "ann@gmail.com", "bob@gmail.com", nil, "", ""},
		{"different people", "ann@acme.io", "bob@other.io", nil, "", ""},
		{"the affiliate's own card", "ann@acme.io", "bob@other.io", []string{"pm_1", "pm_2"}, "pm_2", ReasonSamePaymentMethod},
		{"another card", "ann@acme.io", "bob@other.io", []string{"pm_1"}, "pm_9", ""},
		{"no card to compare", "ann@acme.io", "bob@other.io", []string{"pm_1"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelfReferral(tc.aff, tc.cust, tc.affPM, tc.custPM); got != tc.want {
				t.Fatalf("SelfReferral = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"jane@example.com": "j***@example.com",
		"j@example.com":    "*@example.com",
		"ünal@example.com": "ü***@example.com",
		"nonsense":         "***",
		"@example.com":     "***",
	} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidCode(t *testing.T) {
	for _, c := range []string{"ANN", "ann-2026", "a_b-c", "ABCDEFGH23456789ABCDEFGH23456789"} {
		if !ValidCode(c) {
			t.Errorf("%q should be valid", c)
		}
	}
	for _, c := range []string{"ab", "has space", "ünal", "", "ABCDEFGH23456789ABCDEFGH234567890", "a/b", "a.b"} {
		if ValidCode(c) {
			t.Errorf("%q should be invalid", c)
		}
	}
}

func TestApproveAfter(t *testing.T) {
	earned := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)
	if got := ApproveAfter(earned, 0); !got.Equal(earned) {
		t.Fatalf("a zero hold approves at once, got %v", got)
	}
	if got := ApproveAfter(earned, 30); !got.Equal(earned.AddDate(0, 0, 30)) {
		t.Fatalf("got %v", got)
	}
}
