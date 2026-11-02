// Package taxid checks the identifiers a customer or a seller is taxed under: the Turkish TCKN (an individual) and VKN (a business), whose last digits are
// checksums, and a European VAT id (its shape, and the check digits of the countries whose algorithm is public). It decides nothing about tax: whether an id is
// *required*, whether a wrong one is refused or only warned about, and what an id changes on an invoice are product decisions
// (docs/plans/05-customer-locale-tax-documents.md).
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

// euVAT is the shape of a VAT id, by the prefix VIES uses. The shape only: see vatCheck for the check digits, and VIES is what tells whether the number exists.
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

// ValidEUVATShape reports whether id has the shape of a VAT id of the country its two first letters name (no check digit is looked at: that is ValidEUVAT).
func ValidEUVATShape(id string) bool {
	id = Normalize(id)
	if len(id) < 4 {
		return false
	}
	re, ok := euVAT[id[:2]]
	return ok && re.MatchString(id)
}

// ValidEUVAT reports whether id is a VAT id: it has the shape of ValidEUVATShape and, for a country whose check digits this package knows (vatCheck), they are
// right. A country without an algorithm here (SK, XI) is checked for its shape only. Like every check of this package it is local: it says nothing about whether the
// number exists or belongs to the customer (that is what VIES is for).
func ValidEUVAT(id string) bool {
	id = Normalize(id)
	if !ValidEUVATShape(id) {
		return false
	}
	check, ok := vatCheck[id[:2]]
	return !ok || check(id[2:])
}

// vatCheck holds, by VAT prefix, the check-digit algorithm of the national part of an id (the id without its two letters) that has the shape of euVAT. The
// algorithms are the public ones, ported from and tested against python-stdnum (an independent implementation), and each returns true for a branch it does not check:
//   - a country that is not here (SK, XI) is shape-only;
//   - CZ is checked for the 8 digits of a legal entity and the 9 starting with 6, BG for the 9 digits of a legal entity, LV for the codes of legal persons (the first
//     digit above 3): the numbers made of a person's birth date (CZ 9 and 10 digits, BG 10 digits, LV personal codes) are shape-only.
var vatCheck = map[string]func(n string) bool{
	"AT": func(n string) bool { return mod(6-luhnSum(n[1:8]), 10) == digit(n, 8) },
	"BE": func(n string) bool { return n != "0000000000" && (num(n[:8])+num(n[8:]))%97 == 0 },
	"BG": func(n string) bool {
		if len(n) != 9 {
			return true
		}
		c := weighted(n, 1, 2, 3, 4, 5, 6, 7, 8) % 11
		if c == 10 {
			c = weighted(n, 3, 4, 5, 6, 7, 8, 9, 10) % 11
		}
		return c%10 == digit(n, 8)
	},
	"CY": func(n string) bool {
		s := 0
		for i := 0; i < 8; i++ {
			if d := digit(n, i); i%2 == 0 {
				s += [10]int{1, 0, 5, 7, 9, 13, 15, 17, 19, 21}[d]
			} else {
				s += d
			}
		}
		return n[:2] != "12" && n[8] == byte('A'+s%26)
	},
	"CZ": func(n string) bool {
		switch {
		case len(n) == 8:
			c := mod(11-weighted(n, 8, 7, 6, 5, 4, 3, 2), 11)
			if c == 0 {
				c = 1
			}
			return n[0] != '9' && c%10 == digit(n, 7)
		case len(n) == 9 && n[0] == '6':
			return mod(8-mod(10-weighted(n[1:], 8, 7, 6, 5, 4, 3, 2)%11, 11), 10) == digit(n, 8)
		}
		return true
	},
	"DE": func(n string) bool { return n[0] != '0' && mod11x10(n) },
	"DK": func(n string) bool { return n[0] != '0' && weighted(n, 2, 7, 6, 5, 4, 3, 2, 1)%11 == 0 },
	"EE": func(n string) bool { return weighted(n, 3, 7, 1, 3, 7, 1, 3, 7, 1)%10 == 0 },
	"EL": func(n string) bool {
		s := 0
		for i := 0; i < 8; i++ {
			s = s*2 + digit(n, i)
		}
		return s*2%11%10 == digit(n, 8)
	},
	"ES": esCheck,
	"FI": func(n string) bool { return weighted(n, 7, 9, 10, 5, 8, 4, 2, 1)%11 == 0 },
	"FR": frCheck,
	"HR": mod11x10,
	"HU": func(n string) bool { return weighted(n, 9, 7, 3, 1, 9, 7, 3, 1)%10 == 0 },
	"IE": ieCheck,
	"IT": func(n string) bool {
		office := num(n[7:10])
		return num(n[:7]) != 0 && (office >= 1 && office <= 100 || office == 120 || office == 121 || office == 888 || office == 999) && luhnSum(n) == 0
	},
	"LT": func(n string) bool {
		body := n[:len(n)-1]
		if (len(n) == 9 && n[7] != '1') || (len(n) == 12 && n[10] != '1') {
			return false
		}
		c, c2 := 0, 0
		for i := range body {
			c += (1 + i%9) * digit(n, i)
			c2 += (1 + (i+2)%9) * digit(n, i)
		}
		if c %= 11; c == 10 {
			c = c2
		}
		return c%11%10 == digit(n, len(n)-1)
	},
	"LU": func(n string) bool { return num(n[:6])%89 == num(n[6:]) },
	"LV": func(n string) bool { return n[0] <= '3' || mod(weighted(n, 9, 1, 4, 8, 3, 10, 2, 5, 7, 6, 1), 11) == 3 },
	"MT": func(n string) bool { return n[0] != '0' && weighted(n, 3, 4, 6, 7, 8, 9, 10, 1)%37 == 0 },
	"NL": func(n string) bool {
		if num(n[:9]) == 0 || num(n[10:]) == 0 {
			return false
		}
		if mod(weighted(n, 9, 8, 7, 6, 5, 4, 3, 2, -1), 11) == 0 { // the 11-proof of the BSN of a person
			return true
		}
		r := 0 // ISO 7064 mod 97-10 over "NL" + the number with its letters as numbers (N=23, L=21, B=11)
		for _, c := range "2321" + n[:9] + "11" + n[10:] {
			r = (r*10 + int(c-'0')) % 97
		}
		return r == 1
	},
	"PL": func(n string) bool { return mod(weighted(n, 6, 5, 7, 2, 3, 4, 5, 6, 7, -1), 11) == 0 },
	"PT": func(n string) bool {
		return n[0] != '0' && mod(11-weighted(n, 9, 8, 7, 6, 5, 4, 3, 2), 11)%10 == digit(n, 8)
	},
	"RO": func(n string) bool {
		if n[0] == '0' {
			return false
		}
		body := strings.Repeat("0", 10-len(n)) + n[:len(n)-1]
		return 10*weighted(body, 7, 5, 3, 2, 1, 7, 5, 3, 2)%11%10 == digit(n, len(n)-1)
	},
	"SE": func(n string) bool { return luhnSum(n[:10]) == 0 },
	"SI": func(n string) bool {
		c := 11 - weighted(n, 8, 7, 6, 5, 4, 3, 2)%11
		return n[0] != '0' && c != 11 && c%10 == digit(n, 7)
	},
}

// digit is the value of the i-th character of n, which the shape has made a digit.
func digit(n string, i int) int { return int(n[i] - '0') }

// num is the value of n, which is all digits.
func num(n string) int {
	v := 0
	for i := range n {
		v = v*10 + digit(n, i)
	}
	return v
}

// mod is a % m that is never negative.
func mod(a, m int) int { return (a%m + m) % m }

// weighted is the sum of the digits of n, from the left, times the weights.
func weighted(n string, w ...int) int {
	s := 0
	for i, x := range w {
		s += x * digit(n, i)
	}
	return s
}

// luhnSum is the Luhn sum of the digits of n modulo 10: 0 for a number whose last digit is its check digit.
func luhnSum(n string) int {
	s := 0
	for i := 0; i < len(n); i++ {
		d := digit(n, len(n)-1-i)
		if i%2 == 1 {
			d *= 2
			d = d/10 + d%10
		}
		s += d
	}
	return s % 10
}

// mod11x10 is the ISO 7064 MOD 11,10 check (DE, HR): the last digit is the check digit.
func mod11x10(n string) bool {
	c := 5
	for i := range n {
		t := c
		if t == 0 {
			t = 10
		}
		c = (t*2%11 + digit(n, i)) % 10
	}
	return c == 1
}

// esCheck is the NIF: a DNI (digits and a letter), an NIE (X, Y, Z), a K, L or M number, or the CIF of a company (a Luhn digit or its letter).
func esCheck(n string) bool {
	const letters = "TRWAGMYFPDXBNJZSQVHLCKE"
	switch f := n[0]; {
	case f >= '0' && f <= '9':
		return n[8] == letters[num(n[:8])%23]
	case f == 'X' || f == 'Y' || f == 'Z':
		return n[8] == letters[num(string('0'+f-'X')+n[1:8])%23]
	case f == 'K' || f == 'L' || f == 'M':
		return n[8] == letters[num(n[1:8])%23]
	case strings.IndexByte("ABCDEFGHJNPQRSUVW", f) >= 0:
		c := (10 - luhnSum(n[1:8]+"0")) % 10
		return n[8] == byte('0'+c) || n[8] == "JABCDEFGHI"[c]
	}
	return false
}

// frCheck is the TVA number: a key (two digits, or letters but I and O for the numbers since 2013) and a SIREN, which is a Luhn number unless it starts with 000.
func frCheck(n string) bool {
	const alphabet = "0123456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	i, j := strings.IndexByte(alphabet, n[0]), strings.IndexByte(alphabet, n[1])
	siren := n[2:]
	if i < 0 || j < 0 || (siren[:3] != "000" && luhnSum(siren) != 0) {
		return false
	}
	if i < 10 && j < 10 {
		return i*10+j == num(siren+"12")%97
	}
	c := i*34 + j - 100
	if i < 10 {
		c = i*24 + j - 10
	}
	return (num(siren)+1+c/11)%11 == c%11
}

// ieCheck is the Irish number: seven digits and one or two letters, or the old form of a digit, a letter (or + or *) and five digits and a letter. The letters run
// from W then A to V (no X, Y, Z).
func ieCheck(n string) bool {
	const alphabet = "WABCDEFGHIJKLMNOPQRSTUV"
	for _, c := range n[7:] {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	body, extra := n[:7], 0
	if len(n) == 9 {
		extra = strings.IndexByte(alphabet, n[8])
	}
	if !allDigits(body) { // the old form: the first digit moves to the end
		body = "0" + n[2:7] + n[:1]
		extra = 0
	}
	return n[7] == alphabet[(weighted(body, 8, 7, 6, 5, 4, 3, 2)+9*extra)%23]
}

// Valid reports whether id is a good identifier of the kind.
func Valid(kind Kind, id string) bool {
	switch kind {
	case TCKN:
		return ValidTCKN(id)
	case VKN:
		return ValidVKN(id)
	case EUVAT:
		return ValidEUVAT(id)
	}
	return false
}

// Detect names what an identifier looks like, when it can be told from its shape: 11 digits that pass the TCKN checksum, 10 digits that pass the VKN checksum, a
// VAT id of a known country whose check digits, where this package knows them, are right. ok is false for anything else (a number that fails both checksums is not guessed at).
func Detect(id string) (Kind, bool) {
	switch {
	case ValidTCKN(id):
		return TCKN, true
	case ValidVKN(id):
		return VKN, true
	case ValidEUVAT(id):
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
	HintEUVAT        Hint = "eu_vat"        // a business in the EU has no VAT id of the shape of its country, or one whose check digits are wrong
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
	case euCountries[c] && kind == "business" && !ValidEUVAT(id):
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

// Recognised reports whether id passes one of the checks of this package (a TCKN or VKN checksum, a European VAT id as ValidEUVAT reads it): what the customer's
// tax_id_validated_at records. It says nothing about whether the number exists.
func Recognised(id string) bool {
	_, ok := Detect(id)
	return ok
}
