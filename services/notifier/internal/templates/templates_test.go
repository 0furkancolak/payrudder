package templates

import (
	"strings"
	"testing"
)

func TestRenderAllTemplates(t *testing.T) {
	data := map[string]string{"customer_name": "Ada", "amount": "11.16", "currency": "USD", "invoice_number": "INV-2026-000001",
		"period": "2026-10", "tenant_name": "Acme", "meter_code": "api_calls", "used": "80", "limit": "100", "threshold_pct": "80"}
	if len(Names()) != 3 {
		t.Fatalf("templates = %v", Names())
	}
	for _, n := range Names() {
		live, err := Render(n, data, false)
		if err != nil || live.Subject == "" || strings.Contains(live.Subject, "SANDBOX") || strings.Contains(live.HTML, "SANDBOX") {
			t.Fatalf("%s live: %+v, %v", n, live, err)
		}
		sb, err := Render(n, data, true)
		if err != nil || !strings.HasPrefix(sb.Subject, "[SANDBOX] ") || !strings.Contains(sb.HTML, "SANDBOX") {
			t.Fatalf("%s sandbox: %+v, %v", n, sb, err)
		}
		if !strings.Contains(live.Text, "Ada") {
			t.Errorf("%s text body lacks the customer: %q", n, live.Text)
		}
	}
	r, _ := Render("invoice_paid.v1", data, false)
	if r.Subject != "Receipt for invoice INV-2026-000001" || !strings.Contains(r.HTML, "11.16 USD") {
		t.Fatalf("invoice_paid.v1 = %+v", r)
	}
	if _, err := Render("nope.v1", data, false); err == nil {
		t.Fatal("unknown template must fail")
	}
}

func TestHTMLIsEscaped(t *testing.T) {
	r, err := Render("invoice_paid.v1", map[string]string{"customer_name": "<script>x</script>"}, false)
	if err != nil || strings.Contains(r.HTML, "<script>") {
		t.Fatalf("customer-controlled text must be escaped: %v %q", err, r.HTML)
	}
}
