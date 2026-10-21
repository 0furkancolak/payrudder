package model

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := ParseDay(s)
	if err != nil {
		panic(err)
	}
	return d
}

func days(ts []time.Time) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, FormatDay(t))
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSingleExpenseFallsOnItsDayOnly(t *testing.T) {
	e := &Expense{IncurredOn: day("2026-03-15"), Recurring: RecurNone}
	if got := days(e.Occurrences(day("2026-03-01"), day("2026-03-31"))); !equal(got, []string{"2026-03-15"}) {
		t.Fatalf("inside: %v", got)
	}
	// Both ends of the window are inside it.
	if got := e.Occurrences(day("2026-03-15"), day("2026-03-15")); len(got) != 1 {
		t.Fatalf("one-day window: %v", got)
	}
	if got := e.Occurrences(day("2026-03-16"), day("2026-04-30")); len(got) != 0 {
		t.Fatalf("after: %v", got)
	}
	if got := e.Occurrences(day("2026-01-01"), day("2026-03-14")); len(got) != 0 {
		t.Fatalf("before: %v", got)
	}
}

func TestMonthlyKeepsTheDayAndClampsShortMonths(t *testing.T) {
	e := &Expense{IncurredOn: day("2026-01-31"), Recurring: RecurMonthly}
	got := days(e.Occurrences(day("2026-01-01"), day("2026-05-31")))
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31"}
	if !equal(got, want) {
		t.Fatalf("monthly from the 31st = %v", got)
	}
	// The anchor is the first day, so a leap February is the 29th and March is back on the 31st.
	e = &Expense{IncurredOn: day("2024-01-31"), Recurring: RecurMonthly}
	if got := days(e.Occurrences(day("2024-02-01"), day("2024-03-31"))); !equal(got, []string{"2024-02-29", "2024-03-31"}) {
		t.Fatalf("leap year: %v", got)
	}
}

func TestRecurringStartsInsideTheWindowOnlyFromItsFirstDay(t *testing.T) {
	e := &Expense{IncurredOn: day("2026-03-10"), Recurring: RecurMonthly}
	got := days(e.Occurrences(day("2026-01-01"), day("2026-05-31")))
	if !equal(got, []string{"2026-03-10", "2026-04-10", "2026-05-10"}) {
		t.Fatalf("started mid-window: %v", got)
	}
}

func TestRecurringUntilIsInclusive(t *testing.T) {
	until := day("2026-04-10")
	e := &Expense{IncurredOn: day("2026-01-10"), Recurring: RecurMonthly, RecurringUntil: &until}
	got := days(e.Occurrences(day("2026-01-01"), day("2026-12-31")))
	if !equal(got, []string{"2026-01-10", "2026-02-10", "2026-03-10", "2026-04-10"}) {
		t.Fatalf("until: %v", got)
	}
	until = day("2026-04-09")
	if got := e.Occurrences(day("2026-01-01"), day("2026-12-31")); len(got) != 3 {
		t.Fatalf("day before the fourth: %v", days(got))
	}
}

func TestYearlyAndLeapDay(t *testing.T) {
	e := &Expense{IncurredOn: day("2024-02-29"), Recurring: RecurYearly}
	got := days(e.Occurrences(day("2024-01-01"), day("2028-12-31")))
	want := []string{"2024-02-29", "2025-02-28", "2026-02-28", "2027-02-28", "2028-02-29"}
	if !equal(got, want) {
		t.Fatalf("yearly leap day: %v", got)
	}
}

func TestLongRunningRecurringSkipsToTheWindow(t *testing.T) {
	// Started twenty years ago: the window is found without walking 240 months, and the answer is exact.
	e := &Expense{IncurredOn: day("2006-05-20"), Recurring: RecurMonthly}
	got := days(e.Occurrences(day("2026-10-01"), day("2026-12-31")))
	if !equal(got, []string{"2026-10-20", "2026-11-20", "2026-12-20"}) {
		t.Fatalf("far window: %v", got)
	}
	y := &Expense{IncurredOn: day("2006-05-20"), Recurring: RecurYearly}
	if got := days(y.Occurrences(day("2026-01-01"), day("2026-12-31"))); !equal(got, []string{"2026-05-20"}) {
		t.Fatalf("yearly far window: %v", got)
	}
}

func TestWindowBeforeTheStartHasNothing(t *testing.T) {
	e := &Expense{IncurredOn: day("2026-06-01"), Recurring: RecurMonthly}
	if got := e.Occurrences(day("2025-01-01"), day("2026-05-31")); len(got) != 0 {
		t.Fatalf("before start: %v", days(got))
	}
}

func TestCategories(t *testing.T) {
	for _, c := range Categories {
		if !ValidCategory(c) {
			t.Errorf("%s is listed but not valid", c)
		}
	}
	if ValidCategory("travel") || ValidCategory("") {
		t.Error("an unknown category is valid")
	}
}
