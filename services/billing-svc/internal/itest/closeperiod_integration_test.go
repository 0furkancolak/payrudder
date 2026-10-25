//go:build integration

package itest

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// The operator's close refuses a period that is not one and a period that has not closed yet, each with its own key and code.
func testClosePeriodRefusals(t *testing.T) {
	e := newEnv(t, envOpts{})
	svc := e.api()
	for _, c := range []struct {
		period string
		code   codes.Code
		key    string
	}{
		{"nope", codes.InvalidArgument, "billing.run.bad_period"},
		{"2026-13", codes.InvalidArgument, "billing.run.bad_period"},
		{"2026-12", codes.FailedPrecondition, "billing.run.period_open"}, // the clock is on 5 November: December has not closed
	} {
		_, err := svc.ClosePeriod(opCtx(), &billingv1.ClosePeriodRequest{TenantId: e.tid.String(), PeriodId: c.period})
		if status.Code(err) != c.code || errkey.Of(err) != c.key {
			t.Errorf("ClosePeriod(%q) = %v (key %q), want %v %s", c.period, err, errkey.Of(err), c.code, c.key)
		}
	}
}
