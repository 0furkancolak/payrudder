// Package phone normalises a customer's mobile number to E.164 ("+905321234567"), the form providers that want a GSM number take. It knows no country: a number
// is accepted only in international form (a leading "+" or the "00" international call prefix), because a national spelling ("0532 123 45 67") cannot be told apart
// from another country's without a default region the customer record does not have. Whether the number exists is not checked.
package phone

import "strings"

// E.164 allows at most 15 digits, country code included; fewer than 7 is no number anyone can be called on.
const (
	minDigits = 7
	maxDigits = 15
)

// Normalize returns the number in E.164 form. Spaces, dots, dashes and brackets that people type for readability are dropped. An empty phone is valid and stays
// empty (the field is optional); ok is false for anything else that is not an international number.
func Normalize(raw string) (e164 string, ok bool) {
	s := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\u00a0', '.', '-', '(', ')', '\t':
			return -1
		}
		return r
	}, raw)
	if s == "" {
		return "", true
	}
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "00"):
		s = s[2:]
	default:
		return "", false
	}
	if len(s) < minDigits || len(s) > maxDigits || s[0] == '0' {
		return "", false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return "+" + s, true
}
