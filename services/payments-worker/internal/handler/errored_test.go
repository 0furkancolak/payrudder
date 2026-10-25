package handler

import "testing"

func TestErroredCarriesAKeyForEveryCode(t *testing.T) {
	for _, code := range []string{CodeNotConfigured, CodeTimeout, CodeRejected, CodeCurrency, CodePending} {
		got := errored(false, code, "m").GetPaymentErrored()
		if got.GetCode() != code || got.GetErrorKey() == "" || got.GetErrorKey() == "common.internal" {
			t.Errorf("%s: key %q", code, got.GetErrorKey())
		}
	}
	if got := errored(false, "SOMETHING_NEW", "m").GetPaymentErrored().GetErrorKey(); got != "common.internal" {
		t.Errorf("unknown code: key %q", got)
	}
}
