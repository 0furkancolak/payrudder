package command

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestCreatePackRefusesBadInputBeforeTouchingTheDatabase(t *testing.T) {
	for name, tc := range map[string]struct {
		name           string
		credits, price int64
		currency       string
		validity       int32
		key            string
	}{
		"empty name":      {"", 1, 1, "USD", 0, "billing.credits.bad_pack"},
		"no credits":      {"P", 0, 1, "USD", 0, "billing.credits.bad_pack"},
		"no price":        {"P", 1, 0, "USD", 0, "billing.credits.bad_pack"},
		"unknown money":   {"P", 1, 1, "XXX", 0, "billing.credits.unknown_currency"},
		"negative expiry": {"P", 1, 1, "USD", -1, "billing.credits.bad_validity"},
		"endless expiry":  {"P", 1, 1, "USD", MaxValidityDays + 1, "billing.credits.bad_validity"},
	} {
		_, err := CreatePack(context.Background(), nil, uuid.New(), tc.name, tc.credits, tc.price, tc.currency, tc.validity)
		if got := errkey.Of(err); got != tc.key {
			t.Errorf("%s: key %q, want %q", name, got, tc.key)
		}
	}
}

func TestLowThresholdIsValidated(t *testing.T) {
	for _, bad := range []int64{-1, maxThreshold + 1} {
		if got := errkey.Of(SetLowThreshold(context.Background(), nil, uuid.New(), bad)); got != "billing.credits.bad_threshold" {
			t.Errorf("threshold %d: key %q", bad, got)
		}
	}
}
