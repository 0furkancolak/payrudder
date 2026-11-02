package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
)

// iyzico posts the result of its checkout form and of the 3-D Secure page to the callback URL the page gave it; the route hands the form to payments-worker and
// sends the payer back to the invoice, whatever happened.
func TestAProvidersReturnIsHandedToTheWorkerAndTheBrowserGoesBackToTheInvoice(t *testing.T) {
	f := &fakeWorker{}
	ts := withWorker(t, f, false)
	key := "link:inv-open:1"
	r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/return/"+url.PathEscape(key)+"?lang=tr", url.Values{"token": {"tok-1"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/i/"+tokOpenLive+"?lang=tr" {
		t.Fatalf("return = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("the redirect keeps the private headers: %v", r.Header)
	}
	f.mu.Lock()
	last, body := f.calls[len(f.calls)-1], f.bodies[len(f.bodies)-1]
	f.mu.Unlock()
	form, _ := body["form"].(map[string]any)
	if last != "POST /admin/invoice-payments/return" || body["key"] != key || len(form) != 1 || form["token"].([]any)[0] != "tok-1" {
		t.Errorf("payments-worker was asked %s %v; the key is the attempt's, the form the provider's, and the page's own ?lang= is not part of it", last, body)
	}
	if got := f.tokens[len(f.tokens)-1]; got != "internal-secret" {
		t.Errorf("token = %q", got)
	}

	// A 3-D Secure return carries more fields; the portal has the same route.
	r, _ = send(t, noRedirect(), http.MethodPost, ts.URL+"/c/"+portalTok+"/invoices/inv-open/return/"+url.PathEscape(key), url.Values{"status": {"success"}, "paymentId": {"88"}, "mdStatus": {"1"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/c/"+portalTok+"/invoices/inv-open" {
		t.Errorf("portal return = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	f.mu.Lock()
	form = f.bodies[len(f.bodies)-1]["form"].(map[string]any)
	f.mu.Unlock()
	if len(form) != 3 || form["mdStatus"].([]any)[0] != "1" {
		t.Errorf("the 3-D Secure fields: %v", form)
	}

	// A worker that is down, or a payment it cannot confirm, still sends the payer to the page, which says what the ledger says: the card may have been charged.
	plain, _ := newTestServer(t)
	r, _ = send(t, noRedirect(), http.MethodPost, plain.URL+"/i/"+tokOpenLive+"/return/"+url.PathEscape(key), url.Values{"token": {"tok-1"}}, nil)
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/i/"+tokOpenLive {
		t.Errorf("no worker = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	// A GET is not a return: nothing is posted, nothing is asked.
	n := len(f.callList())
	if r, _ := get(t, ts, "/i/"+tokOpenLive+"/return/"+url.PathEscape(key)); r.StatusCode == http.StatusSeeOther || len(f.callList()) != n {
		t.Errorf("GET return = %d", r.StatusCode)
	}
}

func TestPayStartsWithTheCustomerTheAddressOfThePayerAndACallbackThatNamesTheAttempt(t *testing.T) {
	f := &fakeWorker{pay: map[string]any{"kind": "redirect", "url": "https://cpp.example/?token=tok-1"}}
	ts := withWorker(t, f, false)
	send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay?lang=de", url.Values{"method": {"checkout"}}, nil)
	f.mu.Lock()
	b := f.bodies[len(f.bodies)-1]
	f.mu.Unlock()
	if b["callback_url"] != "https://api.example.test/i/"+tokOpenLive+"/return/{key}?lang=de" {
		t.Errorf("callback_url = %v; the worker puts the attempt's key where {key} is", b["callback_url"])
	}
	if ip, _ := b["payer_ip"].(string); ip == "" {
		t.Errorf("payer_ip = %v; a person is on the page, and the provider's fraud checks want their address", b["payer_ip"])
	}
	if _, ok := b["customer_external_id"]; !ok {
		t.Error("the customer's external id is not sent: the worker could not read the customer")
	}
}

func TestTheBanksPageIsServedAsASandboxedDocument(t *testing.T) {
	page := `<form id="f" action="https://bank.example/3ds" method="post"><script>document.getElementById("f").submit()</script></form>`
	f := &fakeWorker{methods: []map[string]string{{"id": "stored_card", "kind": "stored_card", "provider": "iyzico"}, {"id": "checkout", "kind": "checkout", "provider": "iyzico"}},
		pay: map[string]any{"kind": "html", "html": page}}
	ts := withWorker(t, f, false)
	_, listed := get(t, ts, "/i/"+tokOpenLive+"?lang=tr")
	if !strings.Contains(listed, `value="stored_card"`) || !strings.Contains(listed, "Kayıtlı kartımla öde") {
		t.Errorf("the saved card is offered in the payer's language:\n%s", listed)
	}
	resp, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay", url.Values{"method": {"stored_card"}}, nil)
	if resp.StatusCode != http.StatusOK || body != page {
		t.Fatalf("= %d %q; the bank's page is handed over as it is", resp.StatusCode, body)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-forms allow-scripts", "form-action https:", "default-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("csp %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-same-origin") || strings.Contains(csp, "allow-top-navigation") {
		t.Errorf("csp = %q; the bank's script must not reach this site", csp)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Referrer-Policy") != "no-referrer" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("headers = %v", resp.Header)
	}
}

func TestAProviderThatNeedsMoreAboutTheCustomerIsToldToThePayerAsContactTheSeller(t *testing.T) {
	f := &fakeWorker{pay: map[string]any{"error": "buyer"}, payCode: http.StatusUnprocessableEntity}
	ts := withWorker(t, f, false)
	for lang, want := range map[string]string{"en": "still needs more details about you", "tr": "daha fazla bilgiye", "de": "weitere Angaben zu Ihnen"} {
		r, body := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenLive+"/pay?lang="+lang, url.Values{"method": {"checkout"}}, nil)
		if r.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, want) {
			t.Errorf("%s: %d\n%s", lang, r.StatusCode, body)
		}
	}
}

func TestAPaymentThatFailedSaysWhyInThePayersLanguage(t *testing.T) {
	declined := func(code string) []map[string]any {
		return []map[string]any{{"id": 7, "invoice_id": "inv-open", "provider": "iyzico", "environment": "live", "kind": "charge", "status": "declined", "amount_minor": 5000, "currency": "USD",
			"occurred_at": "2026-11-02T10:00:00Z", "decline_code": code}}
	}
	for lang, want := range map[string]string{"en": "Your card has insufficient funds or limit.", "tr": "Kartınızda yeterli bakiye ya da limit yok.", "de": "Auf Ihrer Karte ist nicht genug Guthaben"} {
		ts := withWorker(t, &fakeWorker{rows: declined("insufficient_funds"), methods: []map[string]string{{"id": "checkout", "kind": "checkout", "provider": "iyzico"}}}, false)
		_, page := get(t, ts, "/i/"+tokOpenLive+"?lang="+lang)
		if !strings.Contains(page, want) || !strings.Contains(page, `value="checkout"`) {
			t.Errorf("%s: the reason and the way to try again are on the page:\n%s", lang, page)
		}
	}
	// A code the page has no sentence for reads as the generic one, and the seller's detail after a colon never reaches the payer.
	ts := withWorker(t, &fakeWorker{rows: declined("buyer_incomplete:phone,tax_id")}, false)
	if _, page := get(t, ts, "/i/"+tokOpenLive); !strings.Contains(page, "still needs more details about you") || strings.Contains(page, "tax_id") {
		t.Errorf("buyer_incomplete:\n%s", page)
	}
	ts = withWorker(t, &fakeWorker{rows: declined("some_new_code")}, false)
	if _, page := get(t, ts, "/i/"+tokOpenLive); !strings.Contains(page, "Your bank or card did not allow the payment.") {
		t.Errorf("an unknown code:\n%s", page)
	}
	// An older failure that a later success followed says nothing.
	ts = withWorker(t, &fakeWorker{rows: declined("")}, false)
	if _, page := get(t, ts, "/i/"+tokOpenLive); strings.Contains(page, "Your bank or card") {
		t.Errorf("no code, no reason:\n%s", page)
	}
}

func TestEveryDeclineWordHasASentenceInEveryLanguage(t *testing.T) {
	words := append(iyzico.DeclineWords(), "buyer_incomplete", "other")
	en := declineStrings["en"]
	for _, lang := range hostedLangs {
		m, ok := declineStrings[lang]
		if !ok {
			t.Fatalf("no decline strings for %s", lang)
		}
		for _, w := range words {
			s := m["decl_"+w]
			if s == "" {
				t.Errorf("%s lacks the sentence for %q", lang, w)
			}
			if lang != "en" && s == en["decl_"+w] {
				t.Errorf("%s: %q is still English", lang, w)
			}
		}
		for k, v := range en {
			if m[k] == "" {
				t.Errorf("%s lacks %q", lang, k)
			}
			if lang != "en" && m[k] == v {
				t.Errorf("%s: %q is still English", lang, k)
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				t.Errorf("%s has a key English lacks: %q", lang, k)
			}
		}
	}
	for k := range en {
		if !strings.HasPrefix(k, "decl_") {
			continue
		}
		w := strings.TrimPrefix(k, "decl_")
		found := w == "other" || w == "buyer_incomplete"
		for _, d := range iyzico.DeclineWords() {
			found = found || d == w
		}
		if !found {
			t.Errorf("%q has a sentence but no adapter produces it", w)
		}
	}
}
