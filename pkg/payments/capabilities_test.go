package payments_test

import (
	"errors"
	"testing"

	"github.com/0furkancolak/quotify/pkg/payments"
)

func TestCapabilities(t *testing.T) {
	c := payments.Capabilities{Currencies: []string{"TRY"}}
	if !c.CanCharge("TRY") || c.CanCharge("USD") {
		t.Error("a provider limited to TRY charges TRY only")
	}
	if !(payments.Capabilities{}).CanCharge("JPY") {
		t.Error("no currency list means any currency")
	}
	for name, tc := range map[string]struct {
		c    payments.Capabilities
		want payments.Owner
	}{
		"renewal and dunning both":   {payments.Capabilities{OwnsRenewal: true, OwnsDunning: true}, payments.OwnerProvider},
		"renewal without dunning":    {payments.Capabilities{OwnsRenewal: true}, payments.OwnerQuotify},
		"nothing (a plain provider)": {payments.Capabilities{}, payments.OwnerQuotify},
	} {
		if got := tc.c.Owner(); got != tc.want {
			t.Errorf("%s: owner %s, want %s", name, got, tc.want)
		}
	}
}

func TestMockPSPCapabilities(t *testing.T) {
	var p payments.Provider = payments.NewMockPSP("http://x", 0)
	if c := p.Capabilities(); c.Name != "mockpsp" || !c.Refunds || c.Owner() != payments.OwnerQuotify {
		t.Errorf("capabilities = %+v", c)
	}
}

func TestRegistry(t *testing.T) {
	r := payments.NewRegistry()
	r.Register("mockpsp", func(settings []byte) (payments.Provider, error) { return payments.NewMockPSP(string(settings), 0), nil })
	p, err := r.Build("mockpsp", []byte("http://psp"))
	if err != nil || p.Name() != "mockpsp" {
		t.Fatalf("Build = %v, %v", p, err)
	}
	if _, err := r.Build("nope", nil); !errors.Is(err, payments.ErrUnknownProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	if got := r.Names(); len(got) != 1 || got[0] != "mockpsp" {
		t.Errorf("Names = %v", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("registering a name twice must panic")
		}
	}()
	r.Register("mockpsp", nil)
}
