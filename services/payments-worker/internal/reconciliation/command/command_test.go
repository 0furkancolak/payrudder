package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
)

func key(err error) string {
	var r *refuse.Error
	if errors.As(err, &r) {
		return r.Key
	}
	return ""
}

// What an operator may say is checked before anything is touched: no store is needed to refuse these.
func TestResolutionsAreCheckedFirst(t *testing.T) {
	ctx, tenant := context.Background(), uuid.New()
	for name, c := range map[string]struct {
		call func() error
		want string
	}{
		"explain without a note": {func() error { _, err := Explain(ctx, Deps{}, tenant, 1, "  ", "", "a"); return err }, "payments.reconcile.note_required"},
		"an owner of a novel":    {func() error { _, err := Assign(ctx, Deps{}, tenant, 1, strings.Repeat("x", 121), "a"); return err }, "payments.reconcile.too_long"},
		"a resolution nobody knows": {func() error {
			_, err := Resolve(ctx, Deps{}, tenant, 1, ResolveInput{Resolution: "fixed", Note: "n"})
			return err
		}, "payments.reconcile.bad_resolution"},
		"cleared is the run's word": {func() error {
			_, err := Resolve(ctx, Deps{}, tenant, 1, ResolveInput{Resolution: "cleared", Note: "n"})
			return err
		}, "payments.reconcile.bad_resolution"},
		"a resolution without a note": {func() error {
			_, err := Resolve(ctx, Deps{}, tenant, 1, ResolveInput{Resolution: NotAnError})
			return err
		}, "payments.reconcile.note_required"},
		"a fact on another resolution": {func() error {
			_, err := Resolve(ctx, Deps{}, tenant, 1, ResolveInput{Resolution: Other, Note: "n", Fact: &Fact{Provider: "stripe", ProviderRef: "x"}})
			return err
		}, "payments.reconcile.bad_ledger_fact"},
		"no ids in bulk": {func() error {
			_, err := ResolveMany(ctx, Deps{}, tenant, nil, ResolveInput{Resolution: Other, Note: "n"})
			return err
		}, "payments.reconcile.too_many"},
		"too many ids in bulk": {func() error {
			_, err := ResolveMany(ctx, Deps{}, tenant, make([]int64, MaxBulk+1), ResolveInput{Resolution: Other, Note: "n"})
			return err
		}, "payments.reconcile.too_many"},
		"a ledger fact in bulk": {func() error {
			_, err := ResolveMany(ctx, Deps{}, tenant, []int64{1}, ResolveInput{Resolution: LedgerFact, Note: "n"})
			return err
		}, "payments.reconcile.bad_resolution"},
		"a run in no environment": {func() error {
			_, err := Run(ctx, Deps{}, RunInput{Tenant: tenant, Environment: "prod", Period: "2026-10"})
			return err
		}, "payments.request.bad_environment"},
		"a fee rule above 100 percent": {func() error { return PutFeeRule(ctx, Deps{}, tenant, feeRule("live", "stripe", "USD", 10001)) }, "payments.settlement.bad_fee_rule"},
		"a fee rule in no currency":    {func() error { return PutFeeRule(ctx, Deps{}, tenant, feeRule("live", "stripe", "ZZZ", 100)) }, "payments.settlement.bad_fee_rule"},
		"a fee rule for no provider":   {func() error { return PutFeeRule(ctx, Deps{}, tenant, feeRule("live", "Stripe~", "USD", 100)) }, "payments.settlement.bad_fee_rule"},
		"an import for no environment": {func() error {
			_, err := ImportCSV(ctx, Deps{}, tenant, "", "stripe", strings.NewReader(""))
			return err
		}, "payments.request.bad_environment"},
		"a report with a bad line": {func() error {
			_, err := ImportCSV(ctx, Deps{}, tenant, "live", "stripe", strings.NewReader("reference,currency,gross,occurred_on\nr,USD,x,2026-10-01\n"))
			return err
		}, "payments.settlement.bad_csv"},
		"a read from the future":          {func() error { _, err := Sync(ctx, Deps{}, tenant, "live", "stripe", farFuture()); return err }, "payments.settlement.bad_since"},
		"a read with no way to build one": {func() error { _, err := Sync(ctx, Deps{}, tenant, "live", "stripe", zeroTime()); return err }, "payments.provider.failed"},
	} {
		if got := key(c.call()); got != c.want {
			t.Errorf("%s: key %q, want %q", name, got, c.want)
		}
	}
}
