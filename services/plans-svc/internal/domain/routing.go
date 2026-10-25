package domain

import (
	"fmt"
	"regexp"

	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Rule sends customers who match to a provider. An empty Country or Currency matches any.
type Rule struct {
	Country, Currency, Provider string
}

// MaxRoutingRules bounds the rule list.
const MaxRoutingRules = 50

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// Capabilities looks a provider up by name (providers.Lookup in production).
type Capabilities func(name string) (payments.Capabilities, bool)

// ValidateRules checks a rule list: known providers; a provider that can charge the rule's currency; no rule that an earlier one makes
// unreachable (the same country and currency, or anything after a catch-all).
func ValidateRules(rules []Rule, caps Capabilities) error {
	var v Violations
	if len(rules) > MaxRoutingRules {
		v.Add("rules", fmt.Sprintf("at most %d rules", MaxRoutingRules))
		return v.Err()
	}
	seen := map[[2]string]int{}
	catchAll := -1
	for i, r := range rules {
		f := fmt.Sprintf("rules[%d]", i)
		if r.Country != "" && !countryCode.MatchString(r.Country) {
			v.Add(f+".country", "an ISO 3166-1 alpha-2 code in capitals, e.g. TR, or empty for any")
		}
		if r.Currency != "" {
			if _, ok := money.LookupCurrency(r.Currency); !ok {
				v.Add(f+".currency", "unsupported currency: use an active ISO 4217 code such as USD, EUR or TRY")
			}
		}
		c, ok := caps(r.Provider)
		switch {
		case !ok:
			v.Add(f+".provider", "unknown provider "+fmt.Sprintf("%q", r.Provider))
		case r.Currency != "" && !c.CanCharge(r.Currency):
			v.Add(f+".provider", fmt.Sprintf("%s cannot charge in %s", r.Provider, r.Currency))
		}
		if catchAll >= 0 {
			v.Add(f, fmt.Sprintf("unreachable: rules[%d] matches everything", catchAll))
			continue
		}
		key := [2]string{r.Country, r.Currency}
		if j, dup := seen[key]; dup {
			v.Add(f, fmt.Sprintf("unreachable: rules[%d] has the same country and currency", j))
		}
		seen[key] = i
		if r.Country == "" && r.Currency == "" {
			catchAll = i
		}
	}
	return v.Err()
}

// Route returns the provider of the first rule that matches, and that rule's position; ok is false when none does.
func Route(rules []Rule, country, currency string) (provider string, index int, ok bool) {
	for i, r := range rules {
		if (r.Country == "" || r.Country == country) && (r.Currency == "" || r.Currency == currency) {
			return r.Provider, i, true
		}
	}
	return "", -1, false
}
