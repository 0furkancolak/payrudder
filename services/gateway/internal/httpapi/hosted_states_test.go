package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// The states a public invoice page used to leave unexplained, and the pages for a link that is gone or a page that cannot be built.
func TestHostedStatesAreExplained(t *testing.T) {
	ts := withWorker(t, &fakeWorker{}, false)

	_, void := get(t, ts, "/i/"+tokVoid)
	if !strings.Contains(void, "This invoice was cancelled. Nothing is due.") || strings.Contains(void, "<button") {
		t.Errorf("a void invoice says it was cancelled and offers nothing to pay:\n%s", void)
	}

	_, bad := get(t, ts, "/i/"+tokUncollect)
	if !strings.Contains(bad, "could not be collected") || strings.Contains(bad, "You can try again") || strings.Contains(bad, "<button") {
		t.Errorf("an invoice that could not be collected must not say 'try again' while offering no way to pay:\n%s", bad)
	}
}

func TestHostedGonePageUsesTheSharedLayout(t *testing.T) {
	ts := withWorker(t, &fakeWorker{}, false)
	r, page := get(t, ts, "/i/nope?lang=tr")
	if r.StatusCode != http.StatusNotFound || !strings.Contains(page, `class="side"`) || !strings.Contains(page, "Bu bağlantı yok ya da süresi dolmuş.") || !strings.Contains(page, `lang="tr"`) {
		t.Errorf("the 404 page is the shared layout, in the visitor's language: %d\n%s", r.StatusCode, page)
	}
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("the 404 page keeps the private headers: %v", r.Header)
	}
}

func TestHostedTroublePageIsAPageNotPlainText(t *testing.T) {
	ts := withWorker(t, &fakeWorker{}, false)
	r, page := get(t, ts, "/i/"+tokBroken+"?lang=de")
	if r.StatusCode != http.StatusServiceUnavailable || r.Header.Get("Retry-After") == "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("a page that cannot be built is a 503 page to retry: %d %v", r.StatusCode, r.Header)
	}
	if !strings.Contains(page, "Diese Seite kann gerade nicht angezeigt werden.") || !strings.Contains(page, `class="side"`) {
		t.Errorf("the 503 page is in the visitor's language and the shared layout:\n%s", page)
	}
}

func TestEveryStateStringIsTranslated(t *testing.T) {
	en := stateStrings["en"]
	for _, lang := range hostedLangs {
		m, ok := stateStrings[lang]
		if !ok {
			t.Fatalf("no state strings for %s", lang)
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
}
