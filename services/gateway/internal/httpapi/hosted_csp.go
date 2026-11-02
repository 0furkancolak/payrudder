package httpapi

import "strings"

// The Content-Security-Policy of a public page lists what that page needs and nothing else: it starts from default-src 'none' (no script, no frame, no font, no
// connection, no object), allows the inline stylesheet, the seller's logo when this page shows one, and the forms this page has. Each view says what it needs
// (hasLogo, formTargets); TestEveryPageRendersInEveryLanguage renders every template and fails when one starts using something its policy forbids.
//
// A provider's script (Paddle.js, Stripe.js) would add script-src, frame-src and connect-src for that provider's hosts on the one page that embeds it, and probably a
// different Referrer-Policy; there is no such page yet (plans 06 to 08), so none is built here.

// Where the forms of a page may post (form-action).
const (
	formsNone = "'none'"
	// formsSelf: the form posts to this origin and the answer is a page or a redirect to a page of this origin.
	formsSelf = "'self'"
	// formsProvider: the pay form posts to this origin and the answer may be an https redirect to the provider's checkout (a browser checks the redirect against
	// form-action too).
	formsProvider = "'self' https:"
)

// pageCSP is the policy of a page; logo says whether it shows the seller's logo, which the gateway serves from its own origin, and forms where its forms post. An
// answer that is not a page passes false and formsNone.
func pageCSP(logo bool, forms string) string {
	var b strings.Builder
	b.WriteString("default-src 'none'; style-src 'unsafe-inline'; ")
	if logo {
		b.WriteString("img-src 'self'; ")
	}
	b.WriteString("base-uri 'none'; form-action " + forms + "; frame-ancestors 'none'")
	return b.String()
}

// hasLogo says whether the page shows the seller's logo, which its policy then lets it load.
func (b pageBase) hasLogo() bool { return b.Logo != "" }

// formTargets is where the forms of the page post; a page has none unless its view says so.
func (b pageBase) formTargets() string { return formsNone }

func (v *invoiceView) formTargets() string {
	if v.Payable && len(v.Methods) > 0 {
		return formsProvider
	}
	return formsNone
}

func (v *adhocView) formTargets() string {
	if v.Consumed {
		return formsNone
	}
	return formsSelf
}

func (v *portalView) formTargets() string {
	if (v.CanBuy && len(v.Packs) > 0) || v.Ask != nil {
		return formsSelf
	}
	return formsNone
}
