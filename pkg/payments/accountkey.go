package payments

import (
	"regexp"
	"strings"
)

// A tenant can connect several accounts at one provider (a Stripe account per region, say). An account is named by its key: the provider's name, or the
// provider's name, a tilde and a label ("stripe~eu"). The key is what is stored, routed to and written to the ledger; only the registry looks through it to
// the provider that builds the adapter. A key without a label is the provider's primary account, so everything stored before labels existed is unchanged.
const labelSeparator = "~"

var labelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// BaseProvider returns the provider an account key belongs to: "stripe~eu" is a Stripe account.
func BaseProvider(key string) string {
	base, _, _ := strings.Cut(key, labelSeparator)
	return base
}

// AccountLabel returns the label of an account key; empty for the primary account of a provider.
func AccountLabel(key string) string {
	_, label, _ := strings.Cut(key, labelSeparator)
	return label
}

// AccountKey joins a provider and a label. An empty label is the primary account.
func AccountKey(provider, label string) string {
	if label == "" {
		return provider
	}
	return provider + labelSeparator + label
}

// ValidAccountLabel reports whether label may be part of an account key: lower-case letters, digits and dashes, up to 31 characters, empty for the primary account.
func ValidAccountLabel(label string) bool { return label == "" || labelPattern.MatchString(label) }

// ValidAccountKey reports whether key is a provider name alone or with a well-formed label.
func ValidAccountKey(key string) bool {
	base, label, hasLabel := strings.Cut(key, labelSeparator)
	if base == "" || (hasLabel && label == "") {
		return false
	}
	return !strings.Contains(label, labelSeparator) && ValidAccountLabel(label)
}
