// Package taxid checks the identifiers a customer or a seller is taxed under: the Turkish TCKN (an individual) and VKN (a business), whose last digits are
// checksums, and the shape of a European VAT id. It decides nothing about tax: whether an id is *required*, whether a wrong one is refused or only warned about,
// and what an id changes on an invoice are product decisions (docs/plans/05-customer-locale-tax-documents.md).
package taxid

import (
	"regexp"
	"strings"
)

// Kind is what an identifier is.
type Kind string

const (
	// TCKN is the 11-digit Turkish citizen number of an individual.
	TCKN Kind = "tckn"
	// VKN is the 10-digit Turkish tax number of a business.
	VKN Kind = "vkn"
	// EUVAT is a European VAT id: two letters of the country (EL for Greece, XI for Northern Ireland) and the national number.
	EUVAT Kind = "eu_vat"
)

// Normalize removes spaces, dots and dashes, which people type for readability, and upper-cases the letters.
func Normalize(id string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '.', '-', ' ':
			return -1
		}
		return r
	}, id))
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// ValidTCKN reports whether id is a Turkish citizen number: 11 digits, the first not zero, and the two check digits right.
//
//	d10 = ((d1+d3+d5+d7+d9)*7 - (d2+d4+d6+d8)) mod 10        d11 = (d1+...+d10) mod 10
func ValidTCKN(id string) bool {
	id = Normalize(id)
	if len(id) != 11 || !allDigits(id) || id[0] == '0' {
		return false
	}
	var d [11]int
	for i := range d {
		d[i] = int(id[i] - '0')
	}
	odd := d[0] + d[2] + d[4] + d[6] + d[8]
	even := d[1] + d[3] + d[5] + d[7]
	if ((odd*7-even)%10+10)%10 != d[9] {
		return false
	}
	sum := 0
	for _, x := range d[:10] {
		sum += x
	}
	return sum%10 == d[10]
}

// ValidVKN reports whether id is a Turkish tax number: 10 digits whose last is the checksum of the first nine (the algorithm of the Revenue Administration).
func ValidVKN(id string) bool {
	id = Normalize(id)
	if len(id) != 10 || !allDigits(id) {
		return false
	}
	sum := 0
	for i := 0; i < 9; i++ {
		tmp := (int(id[i]-'0') + 9 - i) % 10
		v := (tmp << (9 - i)) % 9
		if tmp != 0 && v == 0 {
			v = 9
		}
		sum += v
	}
	return (10-sum%10)%10 == int(id[9]-'0')
}

// euVAT is the shape of the national part of a VAT id, by the prefix VIES uses. It is the shape only: the check digits of the countries that have them are not
// verified (that is what VIES is for), so a well-formed id may still not exist.
var euVAT = map[string]*regexp.Regexp{
	"AT": regexp.MustCompile(`^ATU\d{8}$`),
	"BE": regexp.MustCompile(`^BE[01]\d{9}$`),
	"BG": regexp.MustCompile(`^BG\d{9,10}$`),
	"CY": regexp.MustCompile(`^CY\d{8}[A-Z]$`),
	"CZ": regexp.MustCompile(`^CZ\d{8,10}$`),
	"DE": regexp.MustCompile(`^DE\d{9}$`),
	"DK": regexp.MustCompile(`^DK\d{8}$`),
	"EE": regexp.MustCompile(`^EE\d{9}$`),
	"EL": regexp.MustCompile(`^EL\d{9}$`),
	"ES": regexp.MustCompile(`^ES[A-Z0-9]\d{7}[A-Z0-9]$`),
	"FI": regexp.MustCompile(`^FI\d{8}$`),
	"FR": regexp.MustCompile(`^FR[A-Z0-9]{2}\d{9}$`),
	"HR": regexp.MustCompile(`^HR\d{11}$`),
	"HU": regexp.MustCompile(`^HU\d{8}$`),
	"IE": regexp.MustCompile(`^IE(\d[A-Z0-9+*]\d{5}[A-Z]{1,2}|\d{7}[A-Z]{1,2})$`),
	"IT": regexp.MustCompile(`^IT\d{11}$`),
	"LT": regexp.MustCompile(`^LT(\d{9}|\d{12})$`),
	"LU": regexp.MustCompile(`^LU\d{8}$`),
	"LV": regexp.MustCompile(`^LV\d{11}$`),
	"MT": regexp.MustCompile(`^MT\d{8}$`),
	"NL": regexp.MustCompile(`^NL\d{9}B\d{2}$`),
	"PL": regexp.MustCompile(`^PL\d{10}$`),
	"PT": regexp.MustCompile(`^PT\d{9}$`),
	"RO": regexp.MustCompile(`^RO\d{2,10}$`),
	"SE": regexp.MustCompile(`^SE\d{10}01$`),
	"SI": regexp.MustCompile(`^SI\d{8}$`),
	"SK": regexp.MustCompile(`^SK\d{10}$`),
	"XI": regexp.MustCompile(`^XI(\d{9}|\d{12}|GD\d{3}|HA\d{3})$`),
}

// ValidEUVATShape reports whether id has the shape of a VAT id of the country its two first letters name.
func ValidEUVATShape(id string) bool {
	id = Normalize(id)
	if len(id) < 4 {
		return false
	}
	re, ok := euVAT[id[:2]]
	return ok && re.MatchString(id)
}

// Valid reports whether id is a good identifier of the kind.
func Valid(kind Kind, id string) bool {
	switch kind {
	case TCKN:
		return ValidTCKN(id)
	case VKN:
		return ValidVKN(id)
	case EUVAT:
		return ValidEUVATShape(id)
	}
	return false
}

// Detect names what an identifier looks like, when it can be told from its shape: 11 digits that pass the TCKN checksum, 10 digits that pass the VKN checksum, a
// VAT id of a known country. ok is false for anything else (a number that fails both checksums is not guessed at).
func Detect(id string) (Kind, bool) {
	switch {
	case ValidTCKN(id):
		return TCKN, true
	case ValidVKN(id):
		return VKN, true
	case ValidEUVATShape(id):
		return EUVAT, true
	}
	return "", false
}

// euCountries are the ISO countries whose VAT ids euVAT knows (Greece is EL in a VAT id and GR as a country).
var euCountries = map[string]bool{"AT": true, "BE": true, "BG": true, "CY": true, "CZ": true, "DE": true, "DK": true, "EE": true, "GR": true, "ES": true, "FI": true, "FR": true, "HR": true,
	"HU": true, "IE": true, "IT": true, "LT": true, "LU": true, "LV": true, "MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SE": true, "SI": true, "SK": true}

// Hint is what is odd about a customer's tax id, as the key of a hint the console words under `taxid.hint.*`. It is a warning, never a refusal (the owner's default
// in docs/plans/05-customer-locale-tax-documents.md); the empty Hint says nothing.
type Hint string

const (
	HintNone         Hint = ""
	HintTRIndividual Hint = "tr_individual" // a Turkish individual's id is not a TCKN
	HintTRBusiness   Hint = "tr_business"   // a Turkish business's id is not a VKN
	HintTRAny        Hint = "tr_any"        // a Turkish customer of unknown kind has neither a TCKN nor a VKN
	HintTRIsTCKN     Hint = "tr_is_tckn"    // a Turkish business was given a TCKN
	HintTRIsVKN      Hint = "tr_is_vkn"     // a Turkish individual was given a VKN
	HintEUVAT        Hint = "eu_vat"        // a business in the EU has no VAT id of the shape of its country
)

// Check says what is odd about id for a customer in country (ISO 3166-1 alpha-2, any case) of kind ("individual", "business" or empty for unknown). Only Turkey and
// the countries of euVAT are checked, and the EU only for a business: for anyone else every id is fine. The same rules, with the same keys, as taxIdHint in the console
// (web/admin/src/shared/lib/taxid.ts).
func Check(id, country, kind string) Hint {
	id = Normalize(id)
	if id == "" {
		return HintNone
	}
	c := strings.ToUpper(country)
	switch {
	case c == "TR" && kind == "individual":
		return pick(ValidTCKN(id), ValidVKN(id), HintTRIndividual, HintTRIsVKN)
	case c == "TR" && kind == "business":
		return pick(ValidVKN(id), ValidTCKN(id), HintTRBusiness, HintTRIsTCKN)
	case c == "TR":
		if ValidTCKN(id) || ValidVKN(id) {
			return HintNone
		}
		return HintTRAny
	case euCountries[c] && kind == "business" && !ValidEUVATShape(id):
		return HintEUVAT
	}
	return HintNone
}

// pick is HintNone when the id is the right kind, the specific hint when it is the other Turkish kind, else the general one.
func pick(right, other bool, general, specific Hint) Hint {
	switch {
	case right:
		return HintNone
	case other:
		return specific
	}
	return general
}

// Recognised reports whether id passes one of the checks of this package (a TCKN or VKN checksum, the shape of a European VAT id): what the customer's
// tax_id_validated_at records. It says nothing about whether the number exists.
func Recognised(id string) bool {
	_, ok := Detect(id)
	return ok
}
