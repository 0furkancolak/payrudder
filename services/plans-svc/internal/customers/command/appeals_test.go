package command

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// Everything that can be refused without reading the customer is refused before the database is touched (these commands have none: reaching it would panic). The paths that
// need rows (not blocked, a second open appeal, accepting, deciding twice) are the integration test CustomerBlockAppeals.
func TestAnAppealIsCheckedBeforeAnythingIsRead(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	for name, statement := range map[string]string{"empty": "", "spaces": " \n\t ", "only NUL": "\x00", "too long": strings.Repeat("a", 2001), "too long in wide letters": strings.Repeat("ş", 2001)} {
		_, err := c.SubmitAppeal(ctx, &plansv1.SubmitCustomerBlockAppealRequest{ExternalId: "cus", Statement: statement})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.SubmitAppeal(ctx, &plansv1.SubmitCustomerBlockAppealRequest{Statement: "hello"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("no customer: %v", err)
	}
}

func TestADecisionIsCheckedBeforeAnythingIsRead(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	const id = "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e"
	for name, req := range map[string]*plansv1.DecideCustomerBlockAppealRequest{
		"no customer":              {AppealId: id, Decision: "accepted"},
		"an id that is not an id":  {ExternalId: "cus", AppealId: "x", Decision: "accepted"},
		"an unknown decision":      {ExternalId: "cus", AppealId: id, Decision: "maybe"},
		"a rejection with nothing": {ExternalId: "cus", AppealId: id, Decision: "rejected"},
		"a rejection with spaces":  {ExternalId: "cus", AppealId: id, Decision: "rejected", Answer: "  \n"},
		"an answer too long":       {ExternalId: "cus", AppealId: id, Decision: "accepted", Answer: strings.Repeat("a", 1001)},
	} {
		if _, err := c.DecideAppeal(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}
