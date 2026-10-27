package templates

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// Names lists the available templates.
func Names() []string {
	var out []string
	if err := fs.WalkDir(files, "files", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(p, "files/"), ".html"))
		}
		return err
	}); err != nil {
		panic(err)
	}
	sort.Strings(out)
	return out
}

func TestRenderAllTemplates(t *testing.T) {
	data := map[string]string{"customer_name": "Ada", "amount": "11.16", "currency": "USD", "invoice_number": "INV-2026-000001",
		"period": "2026-10", "tenant_name": "Acme", "meter_code": "api_calls", "used": "80", "limit": "100", "threshold_pct": "80", "plan_code": "pro", "trial_end": "6 Nov 2026, 21:30 (UTC)", "trial_end_date": "6 Nov 2026"}
	if len(Names()) != 5 {
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

func TestTransferMailCarriesTheBankDetails(t *testing.T) {
	data := map[string]string{"customer_name": "Ada", "amount": "1,500.00", "currency": "TRY", "invoice_number": "INV-2026-000007", "period": "2026-10", "tenant_name": "Acme",
		"bank_name": "Example Bank", "account_holder": "Acme Ltd", "iban": "TR330006100519786457841326", "bic": "TCZBTR2A", "reference": "INV-2026-000007"}
	r, err := Render("invoice_transfer.v1", data, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"TR330006100519786457841326", "Example Bank", "Acme Ltd", "TCZBTR2A", "Payment reference", "INV-2026-000007", "1,500.00 TRY"} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("the mail lacks %q: %s", want, r.HTML)
		}
	}
	if r.Subject != "Invoice INV-2026-000007: how to pay by bank transfer" {
		t.Errorf("subject %q", r.Subject)
	}
	failed, _ := Render("payment_failed.v1", data, false)
	if !strings.Contains(failed.HTML, "TR330006100519786457841326") {
		t.Error("a failed payment tells the customer where they can still pay by transfer")
	}
	card, _ := Render("payment_failed.v1", map[string]string{"customer_name": "Ada"}, false)
	if strings.Contains(card.HTML, "bank transfer") {
		t.Error("a customer who did not pay by transfer is not told about one")
	}
}

func TestHTMLIsEscaped(t *testing.T) {
	r, err := Render("invoice_paid.v1", map[string]string{"customer_name": "<script>x</script>"}, false)
	if err != nil || strings.Contains(r.HTML, "<script>") {
		t.Fatalf("customer-controlled text must be escaped: %v %q", err, r.HTML)
	}
}
