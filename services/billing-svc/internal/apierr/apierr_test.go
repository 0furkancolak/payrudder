package apierr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// logged runs fn with a default logger that writes to a buffer and returns what was logged.
func logged(fn func()) string {
	var b strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&b, nil)))
	defer slog.SetDefault(prev)
	fn()
	return b.String()
}

func TestInternalLogsTheCauseAndHidesIt(t *testing.T) {
	cause := errors.New("pq: connection to 10.0.0.3 lost")
	var got error
	out := logged(func() { got = Internal(context.Background(), cause) })
	if status.Code(got) != codes.Internal || strings.Contains(status.Convert(got).Message(), "10.0.0.3") {
		t.Errorf("answer = %v, want a fixed Internal error", got)
	}
	if !strings.Contains(out, "10.0.0.3") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("log = %q, want the cause as an error", out)
	}
}

func TestInternalAnswersUnavailableForATenantThatIsBeingMoved(t *testing.T) {
	var got error
	cause := fmt.Errorf("pgxx: begin: %w", tenantdir.NotServing(tenantdir.Placement{TenantID: uuid.New(), Status: tenantdir.StatusMoving}))
	out := logged(func() { got = Internal(context.Background(), cause) })
	if status.Code(got) != codes.Unavailable || !strings.Contains(status.Convert(got).Message(), "retry is safe") {
		t.Errorf("answer = %v, want Unavailable and a safe retry", got)
	}
	if strings.Contains(out, "level=ERROR") {
		t.Errorf("log = %q: a move is expected, not an error", out)
	}
}

func TestInternalLogsAnAbandonedRequestAsAWarning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := logged(func() { _ = Internal(ctx, context.Canceled) })
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("log = %q, want a warning", out)
	}
}

func TestWrapPassesStatusErrorsAndHidesTheRest(t *testing.T) {
	keyed := errkey.New(codes.NotFound, "billing.invoice.not_found", "invoice not found")
	var got error
	if out := logged(func() { got = Wrap(context.Background(), keyed) }); !errors.Is(got, keyed) || out != "" {
		t.Errorf("a keyed refusal = %v, log %q; want it untouched and unlogged", got, out)
	}
	logged(func() { got = Wrap(context.Background(), errors.New("boom")) })
	if status.Code(got) != codes.Internal {
		t.Errorf("a plain error = %v, want Internal", got)
	}
	logged(func() { got = Wrap(context.Background(), status.Error(codes.Unknown, "who knows")) })
	if status.Code(got) != codes.Internal || strings.Contains(got.Error(), "who knows") {
		t.Errorf("an Unknown status = %v, want it hidden", got)
	}
}
