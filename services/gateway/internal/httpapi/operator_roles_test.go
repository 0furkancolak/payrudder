package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

const roleTenantID = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"

// Every operator route is classed: a GET reads, every other method is listed in operatorClasses, and the list holds no route that is not in the table.
func TestEveryOperatorRouteIsClassed(t *testing.T) {
	seen := map[string]bool{}
	n := 0
	for _, rt := range (&Server{}).routes() {
		if rt.Access != AccessOperator {
			continue
		}
		n++
		key := rt.Method + " " + rt.Path
		seen[key] = true
		switch rt.Method {
		case http.MethodGet, http.MethodHead:
			if _, listed := operatorClasses[key]; listed {
				t.Errorf("%s reads by its method and must not be listed", key)
			}
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if operatorClasses[key] == 0 {
				t.Errorf("%s is not classed: add it to operatorClasses (read or write)", key)
			}
		default:
			t.Errorf("%s: unknown method", key)
		}
	}
	if n < 100 {
		t.Errorf("only %d operator routes seen: the route table was not read", n)
	}
	for key, c := range operatorClasses {
		if !seen[key] {
			t.Errorf("operatorClasses lists %s, which is not an operator route", key)
		}
		if c != opRead && c != opWrite {
			t.Errorf("%s has no class", key)
		}
	}
	if !operatorWrites("POST", "/admin/v1/not-yet-classified") || operatorWrites("GET", "/admin/v1/not-yet-classified") {
		t.Error("an unclassed POST must count as a write, a GET as a read")
	}
}

func TestSupportOperatorReadsAndNeverWrites(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }))
	t.Cleanup(worker.Close)
	u, _ := url.Parse(worker.URL)
	p := newProduct(t, nil, func(d *Deps) {
		d.OperatorEmails = NormalizeOperatorEmails([]string{"boss@example.com", "help@example.com"}) // help is on both lists: support wins
		d.OperatorSupportEmails = NormalizeOperatorEmails([]string{" Help@Example.com "})
		d.PaymentsURL = u
	})
	call := func(token, method, path string) (int, string) {
		resp, m := do(t, p.ts, method, path, token, `{}`)
		return resp.StatusCode, errKeyOf(m)
	}
	for _, path := range []string{"/admin/v1/audit-events", "/admin/v1/support/tickets", "/admin/v1/saas", "/admin/v1/price-change-pause", "/admin/v1/blocklist"} {
		if code, key := call(tokSupport, "GET", path); code == 401 || code == 403 {
			t.Errorf("support GET %s: %d %s", path, code, key)
		}
	}
	// A POST that only reads is open to support.
	preview := "/admin/v1/tenants/" + roleTenantID + "/providers/stripe/subscriptions/sub_1/change-preview"
	if code, key := call(tokSupport, "POST", preview); code == 401 || code == 403 {
		t.Errorf("support POST change-preview: %d %s", code, key)
	}
	writes := [][2]string{{"PUT", "/admin/v1/price-change-pause"}, {"POST", "/admin/v1/blocklist"}, {"DELETE", "/admin/v1/blocklist/x"}, {"PATCH", "/admin/v1/tenants/" + roleTenantID},
		{"POST", "/admin/v1/tenants/" + roleTenantID + "/reconciliation/discrepancies/1/explain"}, {"POST", "/admin/v1/users/two-factor/reset"}}
	for _, w := range writes {
		if code, key := call(tokSupport, w[0], w[1]); code != 403 || key != "gateway.auth.permission_denied" {
			t.Errorf("support %s %s: %d %s, want 403 gateway.auth.permission_denied", w[0], w[1], code, key)
		}
		// The machine token is an admin.
		if code, key := call(operatorToken, w[0], w[1]); key == "gateway.auth.permission_denied" {
			t.Errorf("machine token %s %s: %d %s", w[0], w[1], code, key)
		}
	}
}

func TestAdminOperatorKeepsWritingAndMeNamesTheRole(t *testing.T) {
	p := newProduct(t, nil, func(d *Deps) {
		d.OperatorEmails = NormalizeOperatorEmails([]string{"boss@example.com"})
		d.OperatorSupportEmails = NormalizeOperatorEmails([]string{"help@example.com"})
	})
	for _, tok := range []string{tokOperator, operatorToken} {
		resp, m := do(t, p.ts, "PUT", "/admin/v1/price-change-pause", tok, `{}`)
		if resp.StatusCode == 403 || resp.StatusCode == 401 {
			t.Errorf("admin %s refused: %d %s", tok, resp.StatusCode, errKeyOf(m))
		}
	}
	for tok, want := range map[string]string{tokSupport: "operator_support", tokOperator: "operator_admin", tokAda: ""} {
		_, me := do(t, p.ts, "GET", "/auth/me", tok, "")
		if got, _ := me["operator_role"].(string); got != want {
			t.Errorf("/auth/me operator_role for %s = %q, want %q", tok, got, want)
		}
	}
}
