package payments_test

import (
	"errors"
	"testing"

	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/payments/iyzico"
	"github.com/0furkancolak/quotify/pkg/payments/paddle"
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
