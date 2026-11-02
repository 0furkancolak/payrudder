package iyzico

import (
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// buyerObjects builds the buyer, shipping address and billing address objects of a request from the customer (b, nil for none) and the seller's explicit
// defaults (Settings.Buyer): the customer's value wins and a field the customer lacks is taken from the defaults only when the seller configured one. What is
// then still empty and mandatory is not made up: the answer is a *payments.BuyerIncompleteError naming it.
//
// Mandatory fields (docs.iyzico.com/en/payment-methods/api/non-3ds/non-3ds-implementation/create-payment, 3ds init-3ds and checkout form cf-initialize, read
// 2026-11-02): buyer id, name, surname, identityNumber, email, gsmNumber, registrationAddress, city and country; an address object needs address, contactName, city
// and country. zipCode and ip are optional, so a missing postal code or payer address is left out, not filled in. The shipping address is mandatory only for
// physical items (3ds and checkout form pages); every Payrudder basket item is VIRTUAL, and both addresses are sent as the billing address because the non-3DS
// page lists the shipping address as required.
func (c *Client) buyerObjects(b *payments.Buyer, orderID string) (buyer, address map[string]string, err error) {
	d := c.s.Buyer
	var in payments.Buyer
	if b != nil {
		in = *b
	}
	pick := func(v, def string) string {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
		return strings.TrimSpace(def)
	}
	name, surname := splitName(in.Name)
	if name == "" {
		name = strings.TrimSpace(d.Name)
	}
	if surname == "" {
		surname = strings.TrimSpace(d.Surname)
	}
	street := strings.TrimSpace(strings.Join(nonEmpty(in.Line1, in.Line2), ", "))
	country := strings.TrimSpace(d.Country)
	if in.Country != "" {
		country = countryName(in.Country)
	}
	fields := []struct{ key, record, value string }{
		{"name", "name", name}, {"surname", "surname", surname},
		{"identityNumber", "tax_id", pick(in.TaxID, d.IdentityNumber)},
		{"email", "email", pick(in.Email, d.Email)},
		{"gsmNumber", "phone", pick(in.Phone, d.GSMNumber)},
		{"registrationAddress", "address.line1", pick(street, d.Address)},
		{"city", "address.city", pick(in.City, d.City)},
		{"country", "country", country},
	}
	var missing []string
	buyer = map[string]string{"id": pick(in.ID, orderID)}
	for _, f := range fields {
		if f.value == "" {
			missing = append(missing, f.record)
		}
		buyer[f.key] = f.value
	}
	if len(missing) > 0 {
		return nil, nil, &payments.BuyerIncompleteError{Provider: Name, Missing: missing}
	}
	if zip := pick(in.PostalCode, d.ZipCode); zip != "" {
		buyer["zipCode"] = zip
	}
	if ip := pick(in.IP, d.IP); ip != "" {
		buyer["ip"] = ip
	}
	address = map[string]string{"address": buyer["registrationAddress"], "contactName": name + " " + surname, "city": buyer["city"], "country": buyer["country"]}
	if zip := buyer["zipCode"]; zip != "" {
		address["zipCode"] = zip
	}
	return buyer, address, nil
}

// splitName cuts a full name into the name and the surname iyzico asks for separately: the surname is the last word ("Ali Veli Yılmaz" is Ali Veli, Yılmaz). A
// single word has no surname.
func splitName(full string) (name, surname string) {
	full = strings.TrimSpace(full)
	i := strings.LastIndexByte(full, ' ')
	if i < 0 {
		return full, ""
	}
	return strings.TrimSpace(full[:i]), strings.TrimSpace(full[i+1:])
}

func nonEmpty(v ...string) []string {
	var out []string
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// countryName is the English name of an ISO 3166-1 alpha-2 code ("TR" is "Turkey", as in iyzico's examples). The documentation gives no list of accepted values for
// country (UNVERIFIED); a code that is not a country is passed on as it is.
func countryName(code string) string {
	code = strings.TrimSpace(code)
	r, err := language.ParseRegion(strings.ToUpper(code))
	if err != nil {
		return code
	}
	if n := display.English.Regions().Name(r); n != "" {
		return n
	}
	return code
}

// buyerFieldOfCode is the field of the customer record behind an iyzico general error code about the buyer (docs.iyzico.com/en/add-ons/error-codes, General Error
// Codes, read 2026-11-02): 3 to 5 the email, 8 to 10 the identity number, 25 to 27 the GSM number (27: "A phone number starting with (+90) must be sent").
func buyerFieldOfCode(code string) (string, bool) {
	switch code {
	case "3", "4", "5":
		return "email", true
	case "8", "9", "10":
		return "tax_id", true
	case "25", "26", "27":
		return "phone", true
	}
	return "", false
}
