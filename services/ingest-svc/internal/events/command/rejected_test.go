package command

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCutKeepsRejectedFieldsSmallAndValid(t *testing.T) {
	if got := cut("api_calls"); got != "api_calls" {
		t.Fatalf("a short value changed: %q", got)
	}
	long := strings.Repeat("ö", 500) // multi-byte: cut by characters, never inside one
	got := cut(long)
	if utf8.RuneCountInString(got) != maxRejectedField || !utf8.ValidString(got) {
		t.Fatalf("cut = %d runes, valid %v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	if got := cut("a\x00b"); got != "ab" { // a NUL is valid JSON and cannot go in a Postgres text
		t.Fatalf("NUL kept: %q", got)
	}
}

func TestTrimIsDueOncePerMinutePerTenant(t *testing.T) {
	c := &Commands{trimmed: map[string]time.Time{}}
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	if !c.dueForTrim("a", now) || c.dueForTrim("a", now.Add(30*time.Second)) {
		t.Fatal("the first write trims, the next one within the minute does not")
	}
	if !c.dueForTrim("b", now.Add(time.Second)) {
		t.Fatal("another tenant has its own clock")
	}
	if !c.dueForTrim("a", now.Add(61*time.Second)) {
		t.Fatal("a minute later it is due again")
	}
}
