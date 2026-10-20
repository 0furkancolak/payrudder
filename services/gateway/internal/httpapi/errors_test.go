package httpapi

import (
	"net/http"
	"testing"
)

func TestErrorsCarryAKey(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, http.MethodPost, "/v1/customers", "", `{}`)
	e, _ := m["error"].(map[string]any)
	if resp.StatusCode != http.StatusUnauthorized || e["key"] != "common.unauthenticated" {
		t.Fatalf("no key: %d %v", resp.StatusCode, m)
	}
	// An error that names its own key keeps it.
	resp, m = do(t, ts, http.MethodPost, "/v1/invoices/draft/links", "live-key", "")
	e, _ = m["error"].(map[string]any)
	if resp.StatusCode != http.StatusConflict || e["key"] != "billing.invoice_link.draft_invoice" && e["key"] != "common.failed_precondition" {
		t.Fatalf("draft: %d %v", resp.StatusCode, m)
	}
}
