package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// A signed-in operator is who the audit trail says, whatever the client puts in X-Payrudder-Actor: otherwise operator A could confirm a bank transfer as B. The
// machine token has no person behind it, so the header stays its way to say who acts (payrudderctl, scripts).
func TestAnOperatorSessionIsTheActorWhateverTheHeaderSays(t *testing.T) {
	var mu sync.Mutex
	var actors []string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		actors = append(actors, r.Header.Get("X-Payrudder-Actor"))
		mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(worker.Close)
	u, _ := url.Parse(worker.URL)
	p := newProduct(t, nil, func(d *Deps) {
		d.OperatorEmails = NormalizeOperatorEmails([]string{"boss@example.com"})
		d.PaymentsURL = u
	})
	path := "/admin/v1/tenants/0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e/invoices/0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5f/transfer/receipts"
	send := func(token, header string) string {
		t.Helper()
		req, _ := http.NewRequest("POST", p.ts.URL+path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		if header != "" {
			req.Header.Set("X-Payrudder-Actor", header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		mu.Lock()
		defer mu.Unlock()
		if resp.StatusCode != 200 || len(actors) == 0 {
			t.Fatalf("status %d, worker saw %v", resp.StatusCode, actors)
		}
		got := actors[len(actors)-1]
		actors = nil
		return got
	}
	if got := send(tokOperator, "someone-else@example.com"); got != "boss@example.com" {
		t.Errorf("a signed-in operator was recorded as %q, want their own address", got)
	}
	if got := send(tokOperator, ""); got != "boss@example.com" {
		t.Errorf("a signed-in operator without the header was recorded as %q", got)
	}
	if got := send(operatorToken, "ci-bot"); got != "ci-bot" {
		t.Errorf("the machine token's header was recorded as %q, want ci-bot", got)
	}
}

func TestActorOfPrefersTheSignedInOperator(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Payrudder-Actor", "forged")
	if got := actorOf(r); got != "forged" {
		t.Errorf("machine token: %q", got)
	}
	r = r.WithContext(context.WithValue(r.Context(), operatorKey{}, "boss@example.com"))
	if got := actorOf(r); got != "boss@example.com" {
		t.Errorf("signed-in operator: %q", got)
	}
}
