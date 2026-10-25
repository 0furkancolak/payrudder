package domain

import "testing"

func TestParseID(t *testing.T) {
	if _, err := ParseID("subscription_id", "0b8f6f5e-4b66-4d1c-8b0e-2f4d3c7a9e11"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	_, err := ParseID("subscription_id", "nope")
	if got := fields(t, err); len(got) != 1 || got[0] != "subscription_id" {
		t.Fatalf("violated fields = %v, want [subscription_id]", got)
	}
}
