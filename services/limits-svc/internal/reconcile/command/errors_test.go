package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestReconcileFailureHidesTheCause(t *testing.T) {
	err := reconcileFailed(context.Background(), errors.New("dial tcp 10.0.0.5:6379: connection refused"))
	if status.Code(err) != codes.Unavailable || errkey.Of(err) != "limits.reconcile.unavailable" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(status.Convert(err).Message(), "10.0.0.5") {
		t.Fatalf("the client saw the cause: %q", status.Convert(err).Message())
	}
}
