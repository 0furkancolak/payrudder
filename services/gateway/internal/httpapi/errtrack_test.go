package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errtrack"
)

// trackedGateway installs an error tracker that reports to a fake server and returns what that server received.
func trackedGateway(t *testing.T) (bodies func() []string) {
	t.Helper()
	var got atomic.Pointer[[]string]
	got.Store(&[]string{})
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		for {
			old := got.Load()
			next := append(append([]string(nil), *old...), string(b))
			if got.CompareAndSwap(old, &next) {
				return
			}
		}
	}))
	t.Cleanup(sink.Close)
	tr, err := errtrack.New(errtrack.Options{DSN: "http://key@" + strings.TrimPrefix(sink.URL, "http://") + "/1", Service: "gateway"})
	if err != nil {
		t.Fatal(err)
	}
	errtrack.Install(tr)
	t.Cleanup(func() { errtrack.Install(nil) })
	return func() []string { tr.Flush(2 * time.Second); return *got.Load() }
}

func TestAPanicInAHandlerIsReportedOnceAndAnswered500(t *testing.T) {
	reports := trackedGateway(t)
	s := &Server{d: Deps{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	h := s.recoverMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler broke with qk_live_ab12cd34_s3cr3tS3cr3tS3cr3t")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/customers", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("answer %d %s", rec.Code, rec.Body)
	}
	got := reports()
	if len(got) != 1 {
		t.Fatalf("%d reports for one panic (the 500 it answers must not be reported again), want 1", len(got))
	}
	if !strings.Contains(got[0], `"level":"fatal"`) || !strings.Contains(got[0], "handler broke") || strings.Contains(got[0], "s3cr3t") || !strings.Contains(got[0], `"http.method":"POST"`) {
		t.Errorf("report %s", got[0])
	}
}

func TestAnInternalErrorIsReportedAndNothingLeaksToTheCaller(t *testing.T) {
	reports := trackedGateway(t)
	rec := httptest.NewRecorder()
	writeError(rec, httptest.NewRequest(http.MethodGet, "/v1/invoices", nil), status.Error(codes.Internal, "pq: password authentication failed postgres://u:hunter2@db/x"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "hunter2") || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("answer %d %s", rec.Code, rec.Body)
	}
	got := reports()
	if len(got) != 1 || strings.Contains(got[0], "hunter2") || !strings.Contains(got[0], "password authentication failed") {
		t.Fatalf("reports %q", got)
	}
}

func TestAnswersTheCallerCausedAreNotReported(t *testing.T) {
	reports := trackedGateway(t)
	for _, err := range []error{
		status.Error(codes.NotFound, "no such customer"), status.Error(codes.InvalidArgument, "bad"), status.Error(codes.PermissionDenied, "no"),
		status.Error(codes.Unavailable, "plans-svc is down"), errBodyTooLarge(10),
	} {
		writeError(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/x", nil), err)
	}
	if got := reports(); len(got) != 0 {
		t.Errorf("%d reports for errors that are not the gateway's or its backends' fault: %q", len(got), got)
	}
}
