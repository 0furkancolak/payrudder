package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

func TestRunErrorsHideTheCause(t *testing.T) {
	cause := errors.New("pq: connection to 10.0.0.3 lost")
	for name, c := range map[string]struct {
		err  error
		code codes.Code
		key  string
	}{
		"not closable": {notClosable(context.Background(), uuid.New(), "2026-10", cause), codes.FailedPrecondition, "billing.run.not_closable"},
		"retry later":  {retryLater(context.Background(), uuid.New(), "2026-10", cause), codes.Unavailable, "billing.run.retry_later"},
	} {
		if status.Code(c.err) != c.code || errkey.Of(c.err) != c.key || strings.Contains(status.Convert(c.err).Message(), "10.0.0.3") {
			t.Errorf("%s: %v", name, c.err)
		}
	}
}
