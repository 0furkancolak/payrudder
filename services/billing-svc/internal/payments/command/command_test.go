package command

import (
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/errkey"
)

func TestValidate(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ok := Payment{Method: "bank_transfer", AmountMinor: 100, ReceivedAt: now.Add(-time.Hour)}
	if err := validate(ok, "ref", "note", now); err != nil {
		t.Fatal(err)
	}
	if err := validate(Payment{Method: "cash", AmountMinor: 1}, "", "", now); err != nil {
		t.Fatalf("no date means now: %v", err)
	}
	if err := validate(Payment{Method: "cash", AmountMinor: 1, ReceivedAt: now.Add(clockSkew - time.Second)}, "", "", now); err != nil {
		t.Fatalf("a clock a little ahead is tolerated: %v", err)
	}
	for name, c := range map[string]struct {
		p         Payment
		ref, note string
		key       string
	}{
		"unknown method":  {Payment{Method: "barter", AmountMinor: 1}, "", "", "billing.payment.bad_method"},
		"no method":       {Payment{AmountMinor: 1}, "", "", "billing.payment.bad_method"},
		"zero amount":     {Payment{Method: "cash"}, "", "", "billing.payment.bad_amount"},
		"negative amount": {Payment{Method: "cash", AmountMinor: -1}, "", "", "billing.payment.bad_amount"},
		"in the future":   {Payment{Method: "cash", AmountMinor: 1, ReceivedAt: now.Add(time.Hour)}, "", "", "billing.payment.bad_date"},
		"long reference":  {Payment{Method: "cash", AmountMinor: 1}, strings.Repeat("x", 129), "", "billing.payment.bad_amount"},
		"multiline ref":   {Payment{Method: "cash", AmountMinor: 1}, "a\nb", "", "billing.payment.bad_amount"},
		"long note":       {Payment{Method: "cash", AmountMinor: 1}, "", strings.Repeat("x", 501), "billing.payment.bad_amount"},
	} {
		if got := errkey.Of(validate(c.p, c.ref, c.note, now)); got != c.key {
			t.Errorf("%s: key %q, want %q", name, got, c.key)
		}
	}
}
