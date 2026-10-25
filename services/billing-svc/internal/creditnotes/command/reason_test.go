package command

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// The reason is trimmed first and counted in characters: 500 three-byte characters are fine, 501 are not, and spaces around it do not count.
func TestCheckReasonCountsTrimmedCharacters(t *testing.T) {
	if got, err := checkReason("  goodwill \n"); err != nil || got != "goodwill" {
		t.Errorf("checkReason = %q, %v; want the trimmed reason", got, err)
	}
	if _, err := checkReason(strings.Repeat("\u00e7", 500)); err != nil {
		t.Errorf("500 two-byte characters: %v", err)
	}
	if _, err := checkReason(" " + strings.Repeat("a", 500) + " "); err != nil {
		t.Errorf("500 characters between spaces: %v", err)
	}
	if _, err := checkReason(strings.Repeat("a", 501)); errkey.Of(err) != "billing.credit_note.reason_too_long" {
		t.Errorf("501 characters: %v", err)
	}
}
