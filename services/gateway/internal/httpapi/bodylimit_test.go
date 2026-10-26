package httpapi

import (
	"strings"
	"testing"
)

// A body over the limit is 413 on every route. Six handlers read the body with their own json decoder and answered 400 "must be JSON like ..." for it, so a
// client could not tell a body that is too big from one that is malformed.
func TestABodyOverTheLimitIs413OnEveryJSONRoute(t *testing.T) {
	ts, _ := newTestServer(t)
	huge := `{"scenario":"` + strings.Repeat("x", MaxBodyBytes) + `"}`
	for name, c := range map[string]struct{ method, path, key string }{
		"payment scenario": {"POST", "/v1/sandbox/customers/c1/payment-scenario", "sandbox-key"},
		"upload session":   {"POST", "/v1/assets/uploads", "live-key"},
		"complete file":    {"POST", "/v1/assets/uploads/s1/files/a1/complete", "live-key"},
		"invoice logo":     {"PUT", "/v1/tenant/invoice-logo", "live-key"},
		"close period":     {"POST", "/v1/sandbox/close-period", "sandbox-key"},
		"simulate":         {"POST", "/v1/sandbox/invoices/i1/simulate", "sandbox-key"},
	} {
		resp, m := do(t, ts, c.method, c.path, c.key, huge)
		if resp.StatusCode != 413 || errCode(m) != "REQUEST_TOO_LARGE" {
			t.Errorf("%s: %d %v, want 413 REQUEST_TOO_LARGE", name, resp.StatusCode, m)
		}
	}
	// A body that is merely malformed is still the handler's own 400.
	if resp, _ := do(t, ts, "POST", "/v1/sandbox/customers/c1/payment-scenario", "sandbox-key", `{"scenario":`); resp.StatusCode != 400 {
		t.Errorf("malformed scenario: %d", resp.StatusCode)
	}
}
