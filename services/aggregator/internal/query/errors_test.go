package query

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
)

func TestCannotComputeHidesTheCause(t *testing.T) {
	err := cannotCompute(context.Background(), uuid.New(), "2026-10", errors.New("unknown time zone Mars/Olympus"))
	if status.Code(err) != codes.InvalidArgument || errkey.Of(err) != "aggregator.period.cannot_compute" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(status.Convert(err).Message(), "Mars") {
		t.Fatalf("the client saw the cause: %q", status.Convert(err).Message())
	}
}
