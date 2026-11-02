package mailadmin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/webhooksig"
)

const secret = "whsec_0123456789abcdef"

// notifier is a stand-in for the notifier's signed routes: it verifies the v2 signature (method, path, query, time, body) as the real one does, and answers from fixtures.
func notifier(t *testing.T, answer func(r *http.Request, body []byte) (int, string)) (*Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if webhooksig.VerifyRequest(r.Header.Get(webhooksig.Header()), r.Method, r.URL, body, secret, time.Now(), 0, false) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		code, out := answer(r, body)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(out))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, &seen
}

func TestClientSignsEveryRequestAndReadsTheAnswers(t *testing.T) {
	tid := uuid.New()
	c, seen := notifier(t, func(r *http.Request, body []byte) (int, string) {
		switch {
		case r.URL.Path == "/v1/mail-suppressions" && r.Method == "GET":
			return 200, `{"suppressions":[{"tenant_id":"` + tid.String() + `","address":"ada@example.com","reason":"bounce","source":"msg_1","detail":"550","created_at":"2026-11-02T09:00:00Z"}],"next_after":"ada@example.com"}`
		case strings.HasPrefix(r.URL.Path, "/v1/mail-suppressions/") && r.Method == "DELETE":
			if strings.HasSuffix(r.URL.Path, "gone@example.com") {
				return 404, `{"error":"the address is not on the list"}`
			}
			return 200, `{"removed":true}`
		case r.URL.Path == "/v1/mail-messages":
			return 200, `{"messages":null,"next_after":""}`
		case r.URL.Path == "/v1/mail-previews/price-change":
			if !strings.Contains(string(body), `"plan_code":"pro"`) {
				return 400, `{"error":"a plan_code is required"}`
			}
			return 200, `{"subject":"S","html":"<p>H</p>","text":"T"}`
		}
		return 500, ""
	})
	ctx := context.Background()
	rows, next, err := c.Suppressions(ctx, tid, "", 20)
	if err != nil || len(rows) != 1 || rows[0].Address != "ada@example.com" || rows[0].CreateTime.Year() != 2026 || next != "ada@example.com" {
		t.Fatalf("suppressions: %+v %q %v", rows, next, err)
	}
	if err := c.Unsuppress(ctx, tid, "ada@example.com"); err != nil {
		t.Errorf("lift: %v", err)
	}
	if err := c.Unsuppress(ctx, tid, "gone@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("lifting what is not there: %v", err)
	}
	// An address the URL has to escape is signed as it is sent: the notifier verifies the path it received.
	if err := c.Unsuppress(ctx, tid, "o'brien+tag%41 x@example.com"); err != nil {
		t.Errorf("lifting an address that needs escaping: %v", err)
	}
	if msgs, _, err := c.Messages(ctx, tid, "", 0); err != nil || msgs == nil || len(msgs) != 0 {
		t.Errorf("messages: %v %v (a list, never null)", msgs, err)
	}
	if out, err := c.PreviewPriceChange(ctx, PriceChangePreview{PlanCode: "pro", Currency: "EUR", EffectiveTime: time.Now()}); err != nil || out.Subject != "S" || out.HTML != "<p>H</p>" {
		t.Errorf("preview: %+v %v", out, err)
	}
	var bad BadRequestError
	if _, err := c.PreviewPriceChange(ctx, PriceChangePreview{}); err == nil || !strings.Contains(err.Error(), "plan_code") {
		t.Errorf("a refused preview: %v %v", err, bad)
	}
	if len(*seen) != 7 || !strings.Contains((*seen)[0], "tenant_id="+tid.String()) || !strings.Contains((*seen)[0], "page_size=20") {
		t.Errorf("requests = %v", *seen)
	}
}

func TestClientRefusesWhatCannotWork(t *testing.T) {
	if _, err := New("notanaddress", secret, nil); err == nil {
		t.Error("an address without a scheme")
	}
	if _, err := New("http://notifier:9111", "short", nil); err == nil {
		t.Error("a secret under 16 characters")
	}
	// A wrong secret is a 401 at the notifier: an error here, not an empty list.
	wrong, err := New("http://127.0.0.1:1", secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wrong.Suppressions(context.Background(), uuid.New(), "", 0); err == nil {
		t.Error("an unreachable notifier")
	}
	c, _ := notifier(t, func(*http.Request, []byte) (int, string) { return 200, `{}` })
	c.secret = "another-secret-0123456789"
	if _, _, err := c.Messages(context.Background(), uuid.New(), "", 0); err == nil {
		t.Error("a request signed with the wrong secret was accepted")
	}
}
