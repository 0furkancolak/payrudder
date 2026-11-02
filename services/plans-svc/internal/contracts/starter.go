package contracts

import (
	"embed"
	"strings"
)

// The starter texts: one per kind, in Turkish and English only. A machine translation of a legal text is worse than none, so the other languages are the seller's own.
// Each begins with a notice that it is a template; a seller who removes the notice has taken the text over.
//
//go:embed starter/*.md
var starters embed.FS

// Starter returns the starter text of a kind in a language (tr or en), and the language.
func Starter(kind, locale string) (body, served string, ok bool) {
	if locale != "tr" && locale != "en" {
		return "", "", false
	}
	b, err := starters.ReadFile("starter/" + kind + "." + locale + ".md")
	if err != nil {
		return "", "", false
	}
	return strings.TrimSpace(string(b)), locale, true
}
