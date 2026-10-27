package payments_test

import (
	"errors"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
)

func TestLivemodeOfAnAccountMustBeTheEnvironments(t *testing.T) {
	iy := func(url string) payments.Provider {
		c, err := iyzico.New(iyzico.Settings{APIKey: "a", SecretKey: "s", BaseURL: url}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	pa := func(url string) payments.Provider {
		c, err := paddle.New(paddle.Settings{APIKey: "k", BaseURL: url, WebhookSecret: "w", UsageProductID: "pro_1"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for name, tc := range map[string]struct {
		p           payments.Provider
		environment string
		ok          bool
	}{
		"iyzico default (live host) as live":      {iy(""), "live", true},
		"iyzico live host as sandbox":             {iy(iyzico.LiveURL), "sandbox", false},
		"iyzico sandbox host as live":             {iy(iyzico.SandboxURL), "live", false},
		"iyzico sandbox host as sandbox":          {iy(iyzico.SandboxURL), "sandbox", true},
		"iyzico unknown host (proxy, double)":     {iy("http://127.0.0.1:9999"), "live", true},
		"iyzico unknown host as sandbox":          {iy("http://127.0.0.1:9999"), "sandbox", true},
		"paddle default (live host) as live":      {pa(""), "live", true},
		"paddle sandbox host as live":             {pa(paddle.SandboxURL), "live", false},
		"paddle live host as sandbox":             {pa(paddle.LiveURL), "sandbox", false},
		"paddle sandbox host as sandbox":          {pa(paddle.SandboxURL), "sandbox", true},
		"a provider that cannot tell as anything": {payments.NewMockPSP("http://x", 0), "live", true},
	} {
		err := payments.CheckLivemode(tc.p, tc.environment)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok=%v", name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, payments.ErrLivemodeMismatch) {
			t.Errorf("%s: %v is not ErrLivemodeMismatch", name, err)
		}
	}
}

// The format of a key says its mode where the provider's keys do: a live key is refused in a sandbox account (and a test key in a live one) even when the
// endpoint is one the check cannot tell.
func TestLivemodeOfAKey(t *testing.T) {
	pa := func(key string) payments.Provider {
		c, err := paddle.New(paddle.Settings{APIKey: key, BaseURL: paddle.SandboxURL, WebhookSecret: "w", UsageProductID: "pro_1"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	iy := func(key string) payments.Provider {
		c, err := iyzico.New(iyzico.Settings{APIKey: key, SecretKey: "s", BaseURL: "http://127.0.0.1:9999"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for name, tc := range map[string]struct {
		p           payments.Provider
		environment string
		ok          bool
	}{
		"paddle live key as sandbox":        {pa("pdl_live_apikey_01hx"), "sandbox", false},
		"paddle sandbox key as sandbox":     {pa("pdl_sdbx_apikey_01hx"), "sandbox", true},
		"paddle sandbox key as live":        {pa("pdl_sdbx_apikey_01hx"), "live", false},
		"paddle key of an unknown format":   {pa("whatever"), "sandbox", true},
		"iyzico sandbox key as sandbox":     {iy("sandbox-AbC"), "sandbox", true},
		"iyzico sandbox key as live":        {iy("sandbox-AbC"), "live", false},
		"iyzico key that does not say":      {iy("AbC"), "sandbox", true},
		"iyzico key that does not say live": {iy("AbC"), "live", true},
	} {
		err := payments.CheckLivemode(tc.p, tc.environment)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok=%v", name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, payments.ErrLivemodeMismatch) {
			t.Errorf("%s: %v is not ErrLivemodeMismatch", name, err)
		}
	}
	// Verify refuses a live key for a sandbox account before the provider is asked anything.
	if _, err := payments.Verify(t.Context(), pa("pdl_live_apikey_01hx"), "sandbox"); !errors.Is(err, payments.ErrLivemodeMismatch) {
		t.Errorf("Verify of a live key as sandbox: %v", err)
	}
}
