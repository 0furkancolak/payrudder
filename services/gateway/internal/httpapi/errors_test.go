package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestErrorsCarryAKey(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, http.MethodPost, "/v1/customers", "", `{}`)
	e, _ := m["error"].(map[string]any)
	if resp.StatusCode != http.StatusUnauthorized || e["key"] != "identity.api_key.missing" {
		t.Fatalf("missing api key: %d %v", resp.StatusCode, m)
	}
	// An error that names its own key keeps it.
	resp, m = do(t, ts, http.MethodPost, "/v1/invoices/draft/links", "live-key", "")
	e, _ = m["error"].(map[string]any)
	if resp.StatusCode != http.StatusConflict || e["key"] != "billing.invoice_link.draft_invoice" && e["key"] != "common.failed_precondition" {
		t.Fatalf("draft: %d %v", resp.StatusCode, m)
	}
}

func TestReadsCanBeRevalidated(t *testing.T) {
	ts, _ := newTestServer(t)
	get := func(etag string) *http.Response {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/invoices/inv-1", nil)
		req.Header.Set("Authorization", "Bearer live-key")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	first := get("")
	tag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || !strings.HasPrefix(tag, `W/"`) || first.Header.Get("Cache-Control") != "private, no-cache" || !strings.Contains(first.Header.Get("Vary"), "Authorization") {
		t.Fatalf("first read: %d %v", first.StatusCode, first.Header)
	}
	if again := get(tag); again.StatusCode != http.StatusNotModified {
		t.Errorf("a repeat with the tag: %d", again.StatusCode)
	}
	if other := get(`W/"somethingelse"`); other.StatusCode != http.StatusOK {
		t.Errorf("a different tag: %d", other.StatusCode)
	}
	// A write is never tagged.
	if w, _ := do(t, ts, http.MethodPost, "/v1/customers", "live-key", `{"external_id":"a","name":"A","email":"a@example.com"}`); w.Header.Get("ETag") != "" {
		t.Error("a write response carries an ETag")
	}
}
