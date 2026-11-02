package command

import (
	"testing"

	"github.com/cockroachdb/apd/v3"
	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// An invoice consumes the lots oldest first, each up to what is left to pay; a lot is closed only when the invoice takes all that is left of it.
func TestPlanCreditsUsesTheLotsInOrderUpToTheInvoice(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	lots := []sqlc.ListUnappliedCreditsRow{{ID: a, Remaining: 300}, {ID: b, Remaining: 500}, {ID: c, Remaining: 100}}
	for _, tc := range []struct {
		name    string
		max     int64
		uses    []CreditUse
		applied int64
	}{
		{"less than the first lot", 200, []CreditUse{{a, 200, false}}, 200},
		{"exactly the first lot", 300, []CreditUse{{a, 300, true}}, 300},
		{"into the second", 450, []CreditUse{{a, 300, true}, {b, 150, false}}, 450},
		{"more than all of them", 5000, []CreditUse{{a, 300, true}, {b, 500, true}, {c, 100, true}}, 900},
		{"nothing to pay", 0, nil, 0},
	} {
		uses, applied := PlanCredits(lots, tc.max)
		if applied != tc.applied || len(uses) != len(tc.uses) {
			t.Errorf("%s: applied %d with %v, want %d with %v", tc.name, applied, uses, tc.applied, tc.uses)
			continue
		}
		for i := range uses {
			if uses[i] != tc.uses[i] {
				t.Errorf("%s: use %d is %v, want %v", tc.name, i, uses[i], tc.uses[i])
			}
		}
	}
}

// A line's quantity is shown the way the invoice stores it: the meter's scale, with exactly that many decimals.
func TestQuantityTextHasTheScaleOfTheMeter(t *testing.T) {
	for _, tc := range []struct {
		in    string
		scale int
		want  string
	}{{"1.25", 3, "1.250"}, {"150", 0, "150"}, {"2.505", 3, "2.505"}, {"-30", 0, "-30"}, {"-0.5", 2, "-0.50"}} {
		q, _, err := apd.NewFromString(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := QuantityText(q, tc.scale); err != nil || got != tc.want {
			t.Errorf("QuantityText(%s, %d) = %q, %v; want %q", tc.in, tc.scale, got, err, tc.want)
		}
	}
}
