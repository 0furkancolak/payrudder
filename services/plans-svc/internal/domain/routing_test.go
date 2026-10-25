package domain

import (
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
)

func caps(name string) (payments.Capabilities, bool) { return providers.Lookup(name) }

func TestRoute(t *testing.T) {
	rules := []Rule{
		{Country: "TR", Currency: "TRY", Provider: "iyzico"},
		{Country: "DE", Provider: "paddle"},
		{Currency: "USD", Provider: "paddle"},
		{Provider: "iyzico"},
	}
	for _, c := range []struct {
		country, currency string
		provider          string
		index             int
	}{
		{"TR", "TRY", "iyzico", 0},
		{"TR", "USD", "paddle", 2}, // a Turkish customer paying dollars is not caught by the TR/TRY rule
		{"DE", "EUR", "paddle", 1},
		{"DE", "TRY", "paddle", 1}, // first match wins
		{"FR", "USD", "paddle", 2},
		{"FR", "EUR", "iyzico", 3}, // the catch-all
		{"", "EUR", "iyzico", 3},   // a customer with no country only matches rules that do not ask for one
	} {
		p, i, ok := Route(rules, c.country, c.currency)
		if !ok || p != c.provider || i != c.index {
			t.Errorf("Route(%q,%q) = %q,%d,%v; want %q,%d", c.country, c.currency, p, i, ok, c.provider, c.index)
		}
	}
	if _, i, ok := Route(rules[:2], "FR", "EUR"); ok || i != -1 {
		t.Errorf("no match: %d %v", i, ok)
	}
	if _, _, ok := Route(nil, "TR", "TRY"); ok {
		t.Error("no rules, no match")
	}
}

func TestValidateRules(t *testing.T) {
	good := []Rule{{Country: "TR", Currency: "TRY", Provider: "iyzico"}, {Currency: "USD", Provider: "paddle"}, {Provider: "iyzico"}}
	if err := ValidateRules(good, caps); err != nil {
		t.Fatalf("good rules: %v", err)
	}
	if err := ValidateRules(nil, caps); err != nil {
		t.Fatalf("no rules is valid (everyone goes to the default): %v", err)
	}
	for name, tc := range map[string]struct {
		rules []Rule
		want  string
	}{
		"unknown provider":       {[]Rule{{Provider: "stripe"}}, "unknown provider"},
		"provider cannot charge": {[]Rule{{Currency: "TRY", Provider: "paddle"}}, "paddle cannot charge in TRY"},
		"bad country":            {[]Rule{{Country: "turkey", Provider: "iyzico"}}, "country"},
		"bad currency":           {[]Rule{{Currency: "XXX", Provider: "iyzico"}}, "currency"},
		"duplicate shadowed":     {[]Rule{{Country: "TR", Provider: "iyzico"}, {Country: "TR", Provider: "paddle"}}, "unreachable"},
		"rule after a catch-all": {[]Rule{{Provider: "iyzico"}, {Country: "TR", Provider: "iyzico"}}, "unreachable"},
		"two catch-alls":         {[]Rule{{Provider: "iyzico"}, {Provider: "paddle"}}, "unreachable"},
		"empty provider":         {[]Rule{{Country: "TR"}}, "unknown provider"},
	} {
		err := ValidateRules(tc.rules, caps)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v; want an error mentioning %q", name, err, tc.want)
		}
	}
	many := make([]Rule, MaxRoutingRules+1)
	for i := range many {
		many[i] = Rule{Country: "TR", Provider: "iyzico"}
	}
	if err := ValidateRules(many, caps); err == nil {
		t.Error("too many rules must be refused")
	}
}
