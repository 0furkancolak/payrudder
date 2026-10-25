// Package brand holds the product's name, so that renaming it is a setting and not a search through the code. What a person or a partner system reads
// (the hosted invoice page, the webhook headers, the console) asks this package; internal identifiers (module path, PAYRUDDER_ variables) change with tools/rename.
package brand

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

// defaultName is the product name as people read it.
const defaultName = "Payrudder"

var name atomic.Pointer[string]

// Set makes name the product name ("Acme Billing"). An empty name keeps the default.
func Set(n string) {
	n = strings.TrimSpace(n)
	if n == "" {
		n = defaultName
	}
	name.Store(&n)
}

// Name is the product name as people read it.
func Name() string {
	if n := name.Load(); n != nil {
		return *n
	}
	return defaultName
}

var notSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug is the name in lower case with dashes: "acme-billing". It names things partners type or parse.
func Slug() string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(Name()), "-"), "-")
}

// Header is an HTTP header of the product: Header("Signature") is "Payrudder-Signature", or "Acme-Billing-Signature" after a rename.
func Header(suffix string) string {
	parts := strings.Split(Slug(), "-")
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "-") + "-" + suffix
}

// UserAgent is what outgoing HTTP calls call themselves: "Payrudder-Webhooks/1".
func UserAgent(what string, version int) string {
	return strings.TrimSuffix(Header(what), "-") + "/" + strconv.Itoa(version)
}
