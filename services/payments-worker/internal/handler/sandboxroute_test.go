package handler

import (
	"errors"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
)

// A sandbox command may be served by the tenant's own account at a provider only when that account is the provider's test mode: the mock and a sandbox adapter
// pass; a live endpoint, a live key, a stand-in that is not an account adapter and the live provider do not.
func TestSandboxGuardAdmitsOnlyTestModeAccounts(t *testing.T) {
	mock := payments.NewMockPSP("http://sandbox", 0)
	h := &Handler{Sandbox: mock, Live: &fakeReal{}}
	iy := func(key, url string) payments.PaymentProvider {
		c, err := iyzico.New(iyzico.Settings{APIKey: key, SecretKey: "s", BaseURL: url}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	pa := func(key, url string) payments.PaymentProvider {
		c, err := paddle.New(paddle.Settings{APIKey: key, BaseURL: url, WebhookSecret: "w", UsageProductID: "pro_1"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for name, tc := range map[string]struct {
		p  payments.PaymentProvider
		ok bool
	}{
		"mock-psp":                        {mock, true},
		"iyzico sandbox host":             {iy("sandbox-a", iyzico.SandboxURL), true},
		"iyzico live host":                {iy("a", iyzico.LiveURL), false},
		"iyzico sandbox key, live host":   {iy("sandbox-a", iyzico.LiveURL), false},
		"paddle sandbox":                  {pa("pdl_sdbx_apikey_1", paddle.SandboxURL), true},
		"paddle live key":                 {pa("pdl_live_apikey_1", paddle.SandboxURL), false},
		"paddle live host":                {pa("pdl_sdbx_apikey_1", paddle.LiveURL), false},
		"a provider with no account kind": {&fakeReal{}, false},
	} {
		err := h.guard(env.Sandbox, tc.p)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrWrongProvider)) {
			t.Errorf("%s: %v, want ok=%v", name, err, tc.ok)
		}
	}
}
