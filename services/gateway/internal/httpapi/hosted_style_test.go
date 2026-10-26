package httpapi

import (
	"bytes"
	"strings"
	"testing"
)

// The hosted pages use the console's tokens in both themes and fit a phone, and a promo-code field is a plain text field that password managers leave alone.
func TestHostedPageStyleAndPromoField(t *testing.T) {
	v := &portalView{pageBase: newBase("en"), Seller: "Acme", Customer: "Ada", ShowCredits: true, CanBuy: true, Buy: "/c/x/credits/buy",
		Packs: []portalPackRow{{ID: "p1", Name: "500 gems", Credits: "500", Price: "4.99", Currency: "USD", Valid: "never expires"}}}
	var out bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&out, "portal", v); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"--primary:oklch(", "@media (prefers-color-scheme:dark)", "@media (max-width:480px)", `width=device-width`,
		`name="code"`, `type="text"`, `autocomplete="off"`, `data-1p-ignore`, `data-lpignore="true"`, `data-form-type="other"`, `class="seller">Acme<`} {
		if !strings.Contains(html, want) {
			t.Errorf("the portal page lacks %q", want)
		}
	}
	if strings.Contains(html, "#14171c") || strings.Contains(html, "#f6f7f9") {
		t.Error("the old hard-coded palette is still in the page")
	}
}
