package httpapi

import "testing"

// The policy that goes out with a page is the one its view asks for: a form target only when the page has a form, the logo only when the page shows it.
func TestPagesSendThePolicyTheyNeed(t *testing.T) {
	ts := withWorker(t, &fakeWorker{methods: []map[string]string{{"id": "card", "kind": "checkout"}}}, false)
	const head, tail = "default-src 'none'; style-src 'unsafe-inline'; ", "base-uri 'none'; "
	for _, c := range []struct{ name, path, want string }{
		{"a payable invoice posts to itself and may end at the provider", "/i/" + tokOpenLive, head + tail + "form-action 'self' https:; frame-ancestors 'none'"},
		{"a void invoice has no form", "/i/" + tokVoid, head + tail + "form-action 'none'; frame-ancestors 'none'"},
		{"an ad-hoc link posts to itself", "/i/" + tokAdhoc, head + tail + "form-action 'self'; frame-ancestors 'none'"},
		{"a used ad-hoc link has no form", "/i/" + tokAdhocUsed, head + tail + "form-action 'none'; frame-ancestors 'none'"},
		{"the portal shows the logo and buys a pack on itself", "/c/" + portalTok, head + "img-src 'self'; " + tail + "form-action 'self'; frame-ancestors 'none'"},
		{"a link that is gone", "/i/nope", head + tail + "form-action 'none'; frame-ancestors 'none'"},
	} {
		r, _ := get(t, ts, c.path)
		if got := r.Header.Get("Content-Security-Policy"); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}
