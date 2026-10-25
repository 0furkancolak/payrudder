package httpapi

import (
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseRef(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	for _, tc := range []struct {
		name, value string
		ok          bool
		code        string
	}{
		{"fresh", "ANN." + at(-time.Hour), true, "ANN"},
		{"a code with dashes and dots in the time part only", "ann-2026_x." + at(-24*time.Hour), true, "ann-2026_x"},
		{"a year old", "ANN." + at(-365*24*time.Hour), true, "ANN"},
		{"older than a year", "ANN." + at(-367*24*time.Hour), false, ""},
		{"in the future", "ANN." + at(2*time.Hour), false, ""},
		{"clock skew of seconds", "ANN." + at(10*time.Second), true, "ANN"},
		{"no time", "ANN", false, ""},
		{"time is not a number", "ANN.yesterday", false, ""},
		{"a code that cannot be one", "A B." + at(-time.Hour), false, ""},
		{"too short a code", "AB." + at(-time.Hour), false, ""},
		{"a script", "<script>." + at(-time.Hour), false, ""},
		{"empty", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, ok := parseRef(tc.value, now)
			if ok != tc.ok || code != tc.code {
				t.Fatalf("parseRef(%q) = %q, %v; want %q, %v", tc.value, code, ok, tc.code, tc.ok)
			}
		})
	}
}

func TestLandingAddress(t *testing.T) {
	for in, want := range map[string]string{
		"":                                     "",
		"https://shop.example.com/welcome":     "https://shop.example.com/welcome?ref=ANN",
		"https://shop.example.com/welcome?a=1": "https://shop.example.com/welcome?a=1&ref=ANN",
		"https://shop.example.com/?ref=theirs": "https://shop.example.com/?ref=theirs",
		"javascript:alert(1)":                  "",
		"ftp://shop.example.com":               "",
		"//evil.example":                       "",
		"https://":                             "",
	} {
		if got := landing(in, "ANN"); got != want {
			t.Errorf("landing(%q) = %q, want %q", in, got, want)
		}
	}
}

func cookieNamed(r *http.Response, name string) *http.Cookie {
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestAffiliateLinkSetsTheCookieAndSendsTheVisitorOn(t *testing.T) {
	ts, _ := newTestServer(t)
	fa := lastAffiliates

	// A program with a landing page: the visitor is sent there with ?ref= (their own query is kept), and the cookie holds the code and the time.
	before := time.Now().Add(-2 * time.Second)
	r, _ := get(t, ts, "/r/land")
	if r.StatusCode != http.StatusFound || r.Header.Get("Location") != "https://shop.example.com/welcome?ref=LAND&utm=x" {
		t.Fatalf("/r/land = %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	c := cookieNamed(r, refCookie)
	if c == nil {
		t.Fatal("no referral cookie")
	}
	code, at, ok := parseRef(c.Value, time.Now())
	if !ok || code != "LAND" || at.Before(before) {
		t.Errorf("cookie value %q", c.Value)
	}
	if c.MaxAge != 30*24*3600 || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie attributes = %+v", c)
	}
	if !c.Secure {
		t.Error("the public address is https: the cookie should be Secure")
	}
	if strings.Count(c.Value, ".") != 1 {
		t.Errorf("the cookie holds more than a code and a time: %q", c.Value)
	}
	if h := r.Header.Get("Cache-Control"); h != "no-store" || r.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers = %q %q", h, r.Header.Get("Referrer-Policy"))
	}
	if cl := fa.clicks(); len(cl) != 1 || cl[0].GetCode() != "land" || cl[0].GetSkipCount() {
		t.Errorf("clicks asked for: %v", cl)
	}

	// Without a landing page there is a page that says thank you, in the visitor's language, and points at how the cookie works.
	for lang, want := range map[string]string{"en": "A small cookie now remembers", "tr": "Küçük bir çerez", "de": "Ein kleines Cookie", "fr": "Un petit cookie", "es": "Una pequeña cookie",
		"ar": "ملف تعريف ارتباط صغير"} {
		r, body := get(t, ts, "/r/ANN?lang="+lang)
		if r.StatusCode != http.StatusOK || !strings.Contains(body, want) || !strings.Contains(body, `href="/privacy?lang=`+lang+`"`) {
			t.Errorf("%s: %d\n%s", lang, r.StatusCode, body)
		}
		if lang == "ar" && !strings.Contains(body, `dir="rtl"`) {
			t.Error("the Arabic page is not right to left")
		}
		if cookieNamed(r, refCookie) == nil {
			t.Errorf("%s: no cookie", lang)
		}
	}

	// A code that is not an active partner's: the usual not-found page, no cookie, nothing counted beyond the question.
	for _, code := range []string{"nobody", "xx", "a%20b", "a.b"} {
		r, _ := get(t, ts, "/r/"+code)
		if r.StatusCode != http.StatusNotFound || cookieNamed(r, refCookie) != nil {
			t.Errorf("/r/%s = %d, cookie %v", code, r.StatusCode, cookieNamed(r, refCookie))
		}
	}
	// The program is down: the link is not found rather than half working.
	if r, _ := get(t, ts, "/r/broken"); r.StatusCode != http.StatusNotFound || cookieNamed(r, refCookie) != nil {
		t.Errorf("/r/broken = %d", r.StatusCode)
	}
	// Only a GET.
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/r/ANN", url.Values{}, nil); r.StatusCode != http.StatusMethodNotAllowed && r.StatusCode != http.StatusNotFound {
		t.Errorf("POST /r/ANN = %d", r.StatusCode)
	}
}

func TestReferralCookieFirstTouchAndNoDoubleCount(t *testing.T) {
	ts, _ := newTestServer(t)
	fa := lastAffiliates
	cookie := func(code string, age time.Duration) map[string]string {
		return map[string]string{"Cookie": refCookie + "=" + refValue(code, time.Now().Add(-age))}
	}
	visit := func(path string, hdr map[string]string) *http.Response {
		r, _ := send(t, noRedirect(), http.MethodGet, ts.URL+path, nil, hdr)
		return r
	}

	// The same link again: not counted a second time, and the cookie (and the time the window runs from) is left alone.
	r := visit("/r/ann", cookie("ANN", 5*24*time.Hour))
	if r.StatusCode != http.StatusOK || cookieNamed(r, refCookie) != nil {
		t.Errorf("the same link again: %d, cookie %v", r.StatusCode, cookieNamed(r, refCookie))
	}
	if cl := fa.clicks(); len(cl) != 1 || !cl[0].GetSkipCount() {
		t.Errorf("the same link again was counted: %v", cl)
	}

	// Another partner's link while the first is inside its window: counted for them, but the first touch keeps the cookie.
	r = visit("/r/bob", cookie("ANN", 5*24*time.Hour))
	if cookieNamed(r, refCookie) != nil {
		t.Errorf("a later touch replaced the cookie: %v", cookieNamed(r, refCookie))
	}
	if cl := fa.clicks(); len(cl) != 2 || cl[1].GetSkipCount() {
		t.Errorf("the other partner's click: %v", cl)
	}

	// The first cookie is older than the window of the new link (7 days): it is replaced.
	r = visit("/r/bob", cookie("ANN", 10*24*time.Hour))
	if c := cookieNamed(r, refCookie); c == nil || !strings.HasPrefix(c.Value, "BOB.") || c.MaxAge != 7*24*3600 {
		t.Errorf("an expired first touch was not replaced: %v", c)
	}

	// A garbage cookie is no cookie.
	for _, bad := range []string{"nonsense", "ANN.0", "ANN." + strconv.FormatInt(time.Now().Add(48*time.Hour).Unix(), 10), "<b>.1"} {
		r = visit("/r/ann", map[string]string{"Cookie": refCookie + "=" + bad})
		if cookieNamed(r, refCookie) == nil {
			t.Errorf("a cookie of %q stopped a visit from setting one", bad)
		}
	}
}

func TestReferralOnTheCheckoutPages(t *testing.T) {
	ts, _ := newTestServer(t)
	fa := lastAffiliates

	// ?ref= on a payment page sets the cookie and the page is shown as usual; a code that is not valid does nothing.
	r, body := get(t, ts, "/i/"+goodToken+"?ref=ANN")
	if r.StatusCode != http.StatusOK || !strings.Contains(body, "INV-2026-000007") {
		t.Fatalf("the page = %d", r.StatusCode)
	}
	if c := cookieNamed(r, refCookie); c == nil || !strings.HasPrefix(c.Value, "ANN.") {
		t.Errorf("?ref= did not set the cookie: %v", c)
	}
	r, _ = get(t, ts, "/i/"+goodToken+"?ref=nobody")
	if r.StatusCode != http.StatusOK || cookieNamed(r, refCookie) != nil {
		t.Errorf("an unknown ref: %d, cookie %v", r.StatusCode, cookieNamed(r, refCookie))
	}
	if r, _ := get(t, ts, "/i/"+goodToken+"?ref=%3Cscript%3E"); r.StatusCode != http.StatusOK || cookieNamed(r, refCookie) != nil {
		t.Errorf("a hostile ref: %d", r.StatusCode)
	}
	if r, _ := get(t, ts, "/c/"+portalTok+"?ref=BOB"); r.StatusCode != http.StatusOK || cookieNamed(r, refCookie) == nil {
		t.Errorf("?ref= on the portal = %d", r.StatusCode)
	}
	// A link that does not exist still gives no cookie.
	if r, _ := get(t, ts, "/i/nope?ref=ANN"); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown link = %d", r.StatusCode)
	}

	// The checkout reads the cookie when a payment starts and passes it to billing-svc, with the time of the visit.
	touched := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	hdr := map[string]string{"Cookie": refCookie + "=" + refValue("ANN", touched)}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokOpenSandbox+"/pay", url.Values{"method": {"mock"}}, hdr); r.StatusCode >= 500 {
		t.Fatalf("pay = %d", r.StatusCode)
	}
	billingMu.Lock()
	pay := lastPayment
	billingMu.Unlock()
	if pay.GetReferralCode() != "ANN" || !pay.GetReferralTouchTime().AsTime().Equal(touched) {
		t.Errorf("billing was asked to pay with %v", pay)
	}
	ts2, _ := newTestServer(t) // the pages allow 10 POSTs a minute per address
	if r, _ := send(t, noRedirect(), http.MethodPost, ts2.URL+"/i/"+tokAdhoc+"/start", url.Values{}, hdr); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("start = %d", r.StatusCode)
	}
	billingMu.Lock()
	start := lastStart
	billingMu.Unlock()
	if start.GetReferralCode() != "ANN" || !start.GetReferralTouchTime().AsTime().Equal(touched) || start.GetToken() != tokAdhoc {
		t.Errorf("billing was asked to start with %v", start)
	}
	if r, _ := send(t, noRedirect(), http.MethodPost, ts2.URL+"/c/"+portalTok+"/credits/buy", url.Values{"pack_id": {"pack-1"}}, hdr); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("buy = %d", r.StatusCode)
	}
	purchaseMu.Lock()
	buy := lastPurchase
	purchaseMu.Unlock()
	if buy.GetReferralCode() != "ANN" || !buy.GetReferralTouchTime().AsTime().Equal(touched) {
		t.Errorf("billing was asked to buy with %v", buy)
	}

	// No cookie, a stale one or a forged one: nothing is passed on.
	ts3, _ := newTestServer(t)
	for name, h := range map[string]map[string]string{
		"none":   nil,
		"stale":  {"Cookie": refCookie + "=" + refValue("ANN", time.Now().Add(-400*24*time.Hour))},
		"forged": {"Cookie": refCookie + "=ANN;extra"},
	} {
		send(t, noRedirect(), http.MethodPost, ts3.URL+"/i/"+tokAdhoc+"/start", url.Values{}, h)
		billingMu.Lock()
		got := lastStart
		billingMu.Unlock()
		if name != "forged" && got.GetReferralCode() != "" {
			t.Errorf("%s: billing was handed %q", name, got.GetReferralCode())
		}
	}
	_ = fa
}

func TestPrivacyPageSaysWhatTheCookieHolds(t *testing.T) {
	ts, _ := newTestServer(t)
	for lang, want := range map[string]string{"en": "Only the partner's code and the time", "tr": "Yalnızca ortağın kodunu", "de": "Nur den Code des Partners", "fr": "Seulement le code du partenaire",
		"es": "Solo el código del afiliado", "ar": "رمز الشريك ووقت"} {
		r, body := get(t, ts, "/privacy?lang="+lang)
		body = html.UnescapeString(body)
		if r.StatusCode != http.StatusOK || !strings.Contains(body, want) || !strings.Contains(body, "payrudder_ref") {
			t.Errorf("%s: %d\n%s", lang, r.StatusCode, body)
		}
		if r.Header.Get("X-Robots-Tag") == "" || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'none'") {
			t.Errorf("%s: the page is not locked down like the others", lang)
		}
	}
}

func TestAffiliateStringsAreInEveryLanguage(t *testing.T) {
	for _, lang := range hostedLangs {
		for key, en := range affiliateStrings["en"] {
			if affiliateStrings[lang][key] == "" {
				t.Errorf("%s lacks %s", lang, key)
			}
			if lang != "en" && affiliateStrings[lang][key] == en {
				t.Errorf("%s has the English text for %s", lang, key)
			}
		}
	}
}
