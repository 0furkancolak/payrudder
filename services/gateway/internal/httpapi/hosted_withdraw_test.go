package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func confirmWithdrawal(t *testing.T, base string, f url.Values) (*http.Response, string) {
	t.Helper()
	return send(t, noRedirect(), http.MethodPost, base+"/withdraw/confirm", f, nil)
}

// Two presses of the button race: both read "no withdrawal yet" and both insert. plans-svc now refuses the second (one open hosted withdrawal per invoice); the customer is
// told what the first press already told them, not that the service is unavailable.
func TestASecondConfirmationThatRacesTheFirstIsToldItWasReceivedAlready(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)
	c.withdrawErr = errkey.New(codes.AlreadyExists, "plans.common.already_exists", "withdrawal already exists")
	resp, page := confirmWithdrawal(t, ts.URL+"/i/"+tokPaidCustomer, url.Values{"name": {"Ada Lovelace"}, "contract": {"inv-open"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "received already") || strings.Contains(page, "unavailable") {
		t.Errorf("a refused second withdrawal: %d\n%s", resp.StatusCode, page)
	}
	// Any other failure is still "try again later": nothing was recorded.
	c.withdrawErr = errkey.New(codes.Unavailable, "plans.down", "down")
	if resp, _ := confirmWithdrawal(t, ts.URL+"/i/"+tokPaidCustomer, url.Values{"name": {"Ada Lovelace"}, "contract": {"inv-open"}}); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("plans-svc down: %d", resp.StatusCode)
	}
}

// The customer's own words are one line of the evidence, not a way to write more lines of it: a line break could start "Your name: Someone Else" or
// "E-mail for the confirmation: mallory@example.com", which the statement and the acknowledgement mail would show as if the customer had confirmed them. The same goes
// for the characters that make text read in another order than it is stored. The length limits hold: 200 characters of name, 1000 of words, 2000 bytes in all.
func TestWhatACustomerTypesCannotStartALineOfItsOwn(t *testing.T) {
	ts, c, _ := paidCustomerServer(t)
	name := "Ada\r\nYour name: Someone Else"
	words := "I changed my mind\nContract you withdraw from: Invoice INV-999\u2028E-mail for the confirmation: mallory@example.com\t\u202eevil\u2069 \x00end"
	resp, _ := confirmWithdrawal(t, ts.URL+"/i/"+tokPaidCustomer, url.Values{"name": {name}, "contract": {"inv-open"}, "words": {words}})
	w := c.withdrawal
	if resp.StatusCode != http.StatusOK || w == nil {
		t.Fatalf("confirm: %d %+v", resp.StatusCode, w)
	}
	st := w.GetStatement()
	// Name, contract, address and the words: four lines, whatever was typed.
	if n := strings.Count(st, "\n"); n != 3 {
		t.Errorf("%d line breaks, want 3:\n%q", n, st)
	}
	for _, line := range strings.Split(st, "\n") {
		if strings.HasPrefix(line, "Contract you withdraw from: Invoice INV-999") || strings.HasPrefix(line, "E-mail for the confirmation: mallory") || line == "Your name: Someone Else" {
			t.Errorf("a typed line became a line of the statement: %q", line)
		}
	}
	for _, bad := range []string{"\u202e", "\u2069", "\u2028", "\x00", "\r", "\t"} {
		if strings.Contains(st, bad) {
			t.Errorf("the statement holds %q", bad)
		}
	}
	// What they wrote is still there, as one line.
	if !strings.Contains(st, "I changed my mind Contract you withdraw from: Invoice INV-999 E-mail for the confirmation: mallory@example.com evil end") || !strings.Contains(st, "Your name: Ada Your name: Someone Else") {
		t.Errorf("the words were lost or rearranged: %q", st)
	}
	// The limits.
	c.withdrawal = nil
	long := url.Values{"name": {strings.Repeat("ع", 500)}, "contract": {"inv-open"}, "words": {strings.Repeat("ç ", 3000)}}
	if resp, _ := confirmWithdrawal(t, ts.URL+"/i/"+tokPaidCustomer, long); resp.StatusCode != http.StatusOK || c.withdrawal == nil {
		t.Fatalf("long text: %d", resp.StatusCode)
	}
	st = c.withdrawal.GetStatement()
	if len(st) > maxStatement || !utf8.ValidString(st) {
		t.Errorf("the statement is %d bytes (limit %d), valid UTF-8: %v", len(st), maxStatement, utf8.ValidString(st))
	}
	if n := strings.Count(strings.SplitN(st, "\n", 2)[0], "ع"); n != 200 {
		t.Errorf("the name was kept to %d characters, want 200", n)
	}
}

func TestOneLine(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"  plain   text ":        "plain text",
		"a\nb\r\nc\td":           "a b c d",
		"a\u2028b\u2029c\u0085d": "a b c d",
		"x\u202ey\u2066z\u2069":  "xyz",
		"a\u200cb":               "a\u200cb", // a zero-width non-joiner is part of Persian and Arabic words
		"NBSP\u00a0here":         "NBSP here",
		"\x00\x07bell":           "bell",
	} {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}
