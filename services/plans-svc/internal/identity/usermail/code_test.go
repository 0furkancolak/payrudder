package usermail

import (
	"strings"
	"testing"
)

func TestCodeMailCarriesTheCodeAndNoLink(t *testing.T) {
	for _, l := range Locales {
		subject, text, body := Render(Mail{Kind: KindCode, Locale: l, To: "a@example.com", Code: "012345"})
		if !strings.Contains(subject, "012345") || !strings.Contains(text, "012345") || !strings.Contains(body, "012345") {
			t.Errorf("%s: the code is missing from the mail: %q", l, subject)
		}
		if strings.Contains(text, "http") || strings.Contains(body, "href") || strings.Contains(text, "{code}") {
			t.Errorf("%s: a code mail has no link and no unfilled placeholder: %s", l, text)
		}
	}
}
