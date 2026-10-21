package banktransfer

import (
	"errors"
	"fmt"
	"strings"
)

// ibanLengths are the lengths of the IBANs of the countries that use them (ISO 13616 registry). A country that is not listed passes the
// generic check (15 to 34 characters and the mod 97 test), so a country added to the registry later is not refused.
var ibanLengths = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16, "BG": 22, "BH": 22, "BR": 29, "BY": 28, "CH": 21, "CR": 22, "CY": 28,
	"CZ": 24, "DE": 22, "DK": 18, "DO": 28, "EE": 20, "EG": 29, "ES": 24, "FI": 18, "FO": 18, "FR": 27, "GB": 22, "GE": 22, "GI": 23, "GL": 18,
	"GR": 27, "GT": 28, "HR": 21, "HU": 28, "IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27, "JO": 30, "KW": 30, "KZ": 20, "LB": 28, "LC": 32,
	"LI": 21, "LT": 20, "LU": 20, "LV": 21, "MC": 27, "MD": 24, "ME": 22, "MK": 19, "MR": 27, "MT": 31, "MU": 30, "NL": 18, "NO": 15, "PK": 24,
	"PL": 28, "PS": 29, "PT": 25, "QA": 29, "RO": 24, "RS": 22, "SA": 24, "SC": 31, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "ST": 25, "SV": 28,
	"TL": 23, "TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24, "XK": 20,
}

// ErrIBAN means the IBAN is malformed or its check digits are wrong.
var ErrIBAN = errors.New("banktransfer: not a valid IBAN")

// NormalizeIBAN removes spaces and dashes and upper-cases: "tr33 0006 1005 1978 6457 8413 26" becomes "TR330006100519786457841326".
func NormalizeIBAN(s string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(s))
}

// ValidateIBAN checks an IBAN (any spacing and case) and returns it normalised: the country code and length, then the ISO 7064 mod 97-10 check.
func ValidateIBAN(s string) (string, error) {
	iban := NormalizeIBAN(s)
	if len(iban) < 15 || len(iban) > 34 {
		return "", fmt.Errorf("%w: %d characters", ErrIBAN, len(iban))
	}
	for i, r := range iban {
		switch {
		case i < 2 && (r < 'A' || r > 'Z'):
			return "", fmt.Errorf("%w: it starts with a two-letter country code", ErrIBAN)
		case i >= 2 && i < 4 && (r < '0' || r > '9'):
			return "", fmt.Errorf("%w: the country code is followed by two check digits", ErrIBAN)
		case (r < '0' || r > '9') && (r < 'A' || r > 'Z'):
			return "", fmt.Errorf("%w: only letters and digits", ErrIBAN)
		}
	}
	if want, ok := ibanLengths[iban[:2]]; ok && len(iban) != want {
		return "", fmt.Errorf("%w: a %s IBAN has %d characters, this one has %d", ErrIBAN, iban[:2], want, len(iban))
	}
	// Move the first four characters to the end, turn letters into numbers (A=10 … Z=35) and the remainder by 97 must be 1. The number is
	// reduced piece by piece, so no big integer is needed.
	rearranged := iban[4:] + iban[:4]
	rem := 0
	for _, r := range rearranged {
		if r >= 'A' {
			rem = (rem*100 + int(r-'A') + 10) % 97
		} else {
			rem = (rem*10 + int(r-'0')) % 97
		}
	}
	if rem != 1 {
		return "", fmt.Errorf("%w: the check digits do not match", ErrIBAN)
	}
	return iban, nil
}
