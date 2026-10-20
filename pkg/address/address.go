// Package address turns the parts of a postal address into the lines printed on an invoice.
package address

import "strings"

// Lines is the address as printed: the street lines, then "postal code city", the region and the country code. Empty parts leave no line.
func Lines(line1, line2, city, region, postalCode, country string) []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	add(line1)
	add(line2)
	add(postalCode + " " + city)
	add(region)
	add(country)
	return out
}
