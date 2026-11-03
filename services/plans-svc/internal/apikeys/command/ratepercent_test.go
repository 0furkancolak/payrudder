package command

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A percentage outside 1 to 100 is refused before anything is read or written (the nil pool would panic otherwise), whoever calls Issue.
func TestIssueRefusesARatePercentOutsideOneToHundred(t *testing.T) {
	c := New(nil, nil, nil)
	for _, p := range []int32{-1, 101} {
		_, _, err := c.Issue(context.Background(), uuid.New(), "admin", nil, p)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%d: %v, want InvalidArgument", p, err)
		}
	}
}

func TestRatePtr(t *testing.T) {
	if ratePtr(0) != nil {
		t.Error("0 must be NULL (no own limit)")
	}
	if p := ratePtr(30); p == nil || *p != 30 {
		t.Errorf("ratePtr(30) = %v", p)
	}
}
