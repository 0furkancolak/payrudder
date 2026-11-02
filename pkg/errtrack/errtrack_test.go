package errtrack_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/errtrack"
)

// sink plays the error server: it keeps what arrives.
type sink struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []received
	code int
}

type received struct {
	path   string
	header http.Header
	body   []byte
}

func newSink(t *testing.T) *sink {
	t.Helper()
	s := &sink{code: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, received{path: r.URL.Path, header: r.Header.Clone(), body: b})
		code := s.code
		s.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sink) got() []received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]received(nil), s.reqs...)
}

func (s *sink) dsn(prefix, project string) string {
	return "http://pubkey123@" + strings.TrimPrefix(s.URL, "http://") + prefix + "/" + project
}

func tracker(t *testing.T, s *sink, o errtrack.Options) *errtrack.Tracker {
	t.Helper()
	o.DSN = s.dsn("", "42")
	if o.Service == "" {
		o.Service = "gateway"
	}
	tr, err := errtrack.New(o)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// decode splits an envelope into its header, item header and event.
func decode(t *testing.T, r received) (head, item map[string]any, ev map[string]any) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(r.body))
	sc.Buffer(nil, 1<<20)
	var lines [][]byte
	for sc.Scan() {
		lines = append(lines, append([]byte(nil), sc.Bytes()...))
	}
	if len(lines) != 3 {
		t.Fatalf("an envelope has 3 lines here, got %d: %q", len(lines), r.body)
	}
	for i, dst := range []*map[string]any{&head, &item, &ev} {
		if err := json.Unmarshal(lines[i], dst); err != nil {
			t.Fatalf("line %d is not JSON: %v", i, err)
		}
	}
	if int(item["length"].(float64)) != len(lines[2]) {
		t.Errorf("item length %v, payload is %d bytes", item["length"], len(lines[2]))
	}
	return head, item, ev
}

func exceptionOf(ev map[string]any) map[string]any {
	return ev["exception"].(map[string]any)["values"].([]any)[0].(map[string]any)
}

func TestOffSendsNothing(t *testing.T) {
	s := newSink(t)
	tr, err := errtrack.New(errtrack.Options{})
	if err != nil || tr != nil {
		t.Fatalf("New without a DSN = %v, %v; want nil, nil", tr, err)
	}
	// None of these may panic or send.
	ctx := context.Background()
	tr.Capture(ctx, errors.New("x"))
	tr.CapturePanic(ctx, "x")
	tr.Flush(time.Second)
	errtrack.Install(nil)
	errtrack.Capture(ctx, errors.New("x"))
	errtrack.CapturePanic(ctx, "x")
	errtrack.Flush(time.Second)
	if tr.Enabled() || errtrack.Default() != nil {
		t.Error("tracking is on without a DSN")
	}
	if n := len(s.got()); n != 0 {
		t.Errorf("%d requests with tracking off", n)
	}
}

func TestBadDSN(t *testing.T) {
	for _, dsn := range []string{"not a url", "https://host/42", "ftp://key@host/42", "https://key@host", "https://key:sekret@/42"} {
		_, err := errtrack.New(errtrack.Options{DSN: dsn})
		if err == nil {
			t.Errorf("DSN %q was accepted", dsn)
			continue
		}
		if strings.Contains(err.Error(), "sekret") || strings.Contains(err.Error(), "key@") {
			t.Errorf("the error repeats the DSN: %v", err)
		}
	}
}

func TestCaptureErrorEnvelope(t *testing.T) {
	s := newSink(t)
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	tr, err := errtrack.New(errtrack.Options{
		DSN: "http://pubkey123:legacysecret@" + strings.TrimPrefix(s.URL, "http://") + "/sentry/42", Service: "gateway", Version: "1.2.3", Environment: "kind",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))

	tr.Capture(ctx, fmt.Errorf("charge failed: %w", errors.New("dial postgres://payrudder:hunter2@db/x: key qk_live_ab12cd34_s3cr3tS3cr3tS3cr3t")),
		errtrack.Tag("http.method", "POST"), errtrack.Tag("note", "token=abc123"))
	tr.Flush(2 * time.Second)

	reqs := s.got()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.path != "/sentry/api/42/envelope/" {
		t.Errorf("path %q: the project id goes into /api/<id>/envelope/ after the DSN's own prefix", r.path)
	}
	if r.header.Get("Content-Type") != "application/x-sentry-envelope" {
		t.Errorf("content type %q", r.header.Get("Content-Type"))
	}
	if a := r.header.Get("X-Sentry-Auth"); !strings.Contains(a, "sentry_key=pubkey123") || strings.Contains(a, "legacysecret") {
		t.Errorf("auth header %q", a)
	}
	head, item, ev := decode(t, r)
	if item["type"] != "event" || head["event_id"] != ev["event_id"] || len(ev["event_id"].(string)) != 32 {
		t.Errorf("headers %v %v, event id %v", head, item, ev["event_id"])
	}
	if strings.Contains(head["dsn"].(string), "legacysecret") || !strings.Contains(head["dsn"].(string), "pubkey123@") {
		t.Errorf("the envelope's dsn %q must carry the public key and no secret", head["dsn"])
	}
	for k, want := range map[string]string{"platform": "go", "level": "error", "release": "1.2.3", "environment": "kind", "logger": "gateway", "timestamp": "2026-11-02T10:00:00.000Z"} {
		if ev[k] != want {
			t.Errorf("%s = %v, want %v", k, ev[k], want)
		}
	}
	tags := ev["tags"].(map[string]any)
	if tags["service"] != "gateway" || tags["http.method"] != "POST" || tags["trace_id"] != tid.String() || tags["note"] != "token=[REDACTED]" {
		t.Errorf("tags %v", tags)
	}
	ex := exceptionOf(ev)
	if ex["type"] != "*errors.errorString" {
		t.Errorf("type %v: the type of the root cause groups better than fmt.wrapError", ex["type"])
	}
	val := ex["value"].(string)
	for _, secret := range []string{"hunter2", "s3cr3tS3cr3t"} {
		if strings.Contains(val, secret) || strings.Contains(string(r.body), secret) {
			t.Errorf("secret %q left the process: %q", secret, val)
		}
	}
	if !strings.Contains(val, "charge failed") || !strings.Contains(val, "[REDACTED]") {
		t.Errorf("value %q", val)
	}
	frames := ex["stacktrace"].(map[string]any)["frames"].([]any)
	last := frames[len(frames)-1].(map[string]any)
	if !strings.Contains(last["function"].(string), "TestCaptureErrorEnvelope") || last["in_app"] != true || !strings.HasSuffix(last["filename"].(string), "errtrack_test.go") {
		t.Errorf("the innermost frame (last) is %v", last)
	}
}

//go:noinline
func boom() { panic("index out of range with qk_live_ab12cd34_s3cr3tS3cr3tS3cr3t") }

func guarded(ctx context.Context, tr *errtrack.Tracker) {
	defer func() {
		if p := recover(); p != nil {
			tr.CapturePanic(ctx, p)
		}
	}()
	boom()
}

func TestPanicIsReportedOnceWithItsOwnStack(t *testing.T) {
	s := newSink(t)
	tr := tracker(t, s, errtrack.Options{})
	guarded(context.Background(), tr)
	guarded(context.Background(), tr) // the same place again: not reported twice
	tr.Flush(2 * time.Second)

	reqs := s.got()
	if len(reqs) != 1 {
		t.Fatalf("%d reports for one panic site, want 1", len(reqs))
	}
	_, _, ev := decode(t, reqs[0])
	if ev["level"] != "fatal" {
		t.Errorf("level %v", ev["level"])
	}
	ex := exceptionOf(ev)
	if ex["type"] != "panic: string" || strings.Contains(ex["value"].(string), "s3cr3t") || ex["mechanism"].(map[string]any)["handled"] != false {
		t.Errorf("exception %v", ex)
	}
	frames := ex["stacktrace"].(map[string]any)["frames"].([]any)
	if top := frames[len(frames)-1].(map[string]any)["function"].(string); top != "boom" {
		t.Errorf("the top frame is %q, want the function that panicked (boom); the runtime's panic frames must be cut off", top)
	}
	var names []string
	for _, f := range frames {
		names = append(names, f.(map[string]any)["function"].(string))
	}
	if !strings.Contains(strings.Join(names, " "), "guarded") {
		t.Errorf("the caller is missing from %v", names)
	}
	if fp := ev["fingerprint"].([]any); len(fp) != 1 || len(fp[0].(string)) != 40 {
		t.Errorf("fingerprint %v", fp)
	}
}

func TestNilDereferenceStackStartsAtTheLine(t *testing.T) {
	s := newSink(t)
	tr := tracker(t, s, errtrack.Options{})
	func() {
		defer func() { tr.CapturePanic(context.Background(), recover()) }()
		var m map[string]*int
		_ = *m["x"]
	}()
	tr.Flush(2 * time.Second)
	_, _, ev := decode(t, s.got()[0])
	frames := exceptionOf(ev)["stacktrace"].(map[string]any)["frames"].([]any)
	if top := frames[len(frames)-1].(map[string]any); strings.HasPrefix(top["function"].(string), "runtime.") || top["in_app"] != true {
		t.Errorf("top frame %v is the runtime's, not the line of the nil dereference", top)
	}
}

func TestSameErrorWithinAMinuteAndAfter(t *testing.T) {
	s := newSink(t)
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	tr := tracker(t, s, errtrack.Options{Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }})
	report := func(id int) {
		tr.Capture(context.Background(), fmt.Errorf("customer %d not found", id))
		tr.Flush(2 * time.Second)
	}
	report(1234567)
	report(7654321) // another id in the text is the same problem
	if n := len(s.got()); n != 1 {
		t.Fatalf("%d reports within a minute, want 1", n)
	}
	mu.Lock()
	now = now.Add(61 * time.Second)
	mu.Unlock()
	report(1234567)
	if n := len(s.got()); n != 2 {
		t.Errorf("%d reports after the window, want 2", n)
	}
}

func TestAtMostSixtyAMinute(t *testing.T) {
	s := newSink(t)
	tr := tracker(t, s, errtrack.Options{})
	letters := "ghijklmnopqrstuvwxyz"
	for i := 0; i < 100; i++ {
		tr.Capture(context.Background(), fmt.Errorf("failure %c%c", letters[i%20], letters[i/20]))
		tr.Flush(2 * time.Second)
	}
	if n := len(s.got()); n != 60 {
		t.Errorf("%d reports in one minute, want the cap of 60", n)
	}
}

func TestRecoverReportsFlushesAndPanicsAgain(t *testing.T) {
	s := newSink(t)
	errtrack.Install(tracker(t, s, errtrack.Options{}))
	t.Cleanup(func() { errtrack.Install(nil) })

	var again any
	func() {
		defer func() { again = recover() }()
		func() {
			defer errtrack.Recover(context.Background())
			panic("worker died")
		}()
	}()
	if again != "worker died" {
		t.Errorf("the panic was not raised again with its value: %v", again)
	}
	// Recover flushed before panicking: no Flush call here.
	if n := len(s.got()); n != 1 {
		t.Errorf("%d reports, want 1 (and already sent)", n)
	}

	// Without a panic Recover is a no-op.
	func() { defer errtrack.Recover(context.Background()) }()
}

func TestAbortHandlerIsNotAFailure(t *testing.T) {
	s := newSink(t)
	tr := tracker(t, s, errtrack.Options{})
	tr.CapturePanic(context.Background(), http.ErrAbortHandler)
	tr.Flush(time.Second)
	if n := len(s.got()); n != 0 {
		t.Errorf("%d reports for http.ErrAbortHandler", n)
	}
}

func TestADownOrRefusingServerDoesNotHurt(t *testing.T) {
	s := newSink(t)
	s.code = http.StatusInternalServerError
	var logs bytes.Buffer
	tr := tracker(t, s, errtrack.Options{Log: slog.New(slog.NewTextHandler(&logs, nil))})
	tr.Capture(context.Background(), errors.New("one"))
	tr.Flush(2 * time.Second)
	if !strings.Contains(logs.String(), "refused") || strings.Contains(logs.String(), "pubkey123") {
		t.Errorf("log %q: say that the server refused, never print the key", logs.String())
	}

	s.Close()
	logs.Reset()
	tr.Capture(context.Background(), errors.New("two"))
	tr.Flush(5 * time.Second)
	if !strings.Contains(logs.String(), "not sent") || strings.Contains(logs.String(), "pubkey123") {
		t.Errorf("log %q", logs.String())
	}
}
